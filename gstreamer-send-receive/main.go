// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

//go:build !js
// +build !js

// gstreamer-receive is a simple application that shows how to receive media using Pion WebRTC and play live using GStreamer.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

var peerConnection *webrtc.PeerConnection = nil

// Prepare the configuration
var config = webrtc.Configuration{
	ICEServers: []webrtc.ICEServer{
		{
			URLs: []string{"stun:stun.l.google.com:19302"},
		},
	},
}

const audioSrc = "alsasrc device=hw:0,0 ! audioconvert ! audioresample"

type lock struct {
	mu     sync.Mutex
	locked bool
}

var globalLock = &lock{}

func (l *lock) acquireLock() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.locked {
		return false
	}
	l.locked = true
	return true
}

func (l *lock) releaseLock() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.locked {
		return false
	}
	l.locked = false
	return true
}

func createPeerConnection(offer *webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	if peerConnection != nil {
		if err := peerConnection.GracefulClose(); err != nil {
			panic(err)
		}
	}

	// Create a new RTCPeerConnection
	peerConnection, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create peer connection: %w", err)
	}

	// Set the handler for ICE connection state
	// This will notify you when the peer has connected/disconnected
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		fmt.Printf("Connection State has changed %s \n", connectionState.String())
	})

	audioTrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: "audio/opus"}, "audio", "pion1")
	if err != nil {
		return nil, fmt.Errorf("failed to create audio track: %w", err)
	}
	_, err = peerConnection.AddTrack(audioTrack)
	if err != nil {
		return nil, fmt.Errorf("failed to add audio track to peer: %w", err)
	}

	// Set a handler for when a new remote track starts, this handler creates a gstreamer pipeline
	// for the given codec
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		fmt.Printf("Peer has provided track of type %d\n", track.PayloadType())

		if track.Kind() == webrtc.RTPCodecTypeVideo {
			// Send a PLI on an interval so that the publisher is pushing a keyframe every rtcpPLIInterval
			go func() {
				ticker := time.NewTicker(time.Second * 3)
				for range ticker.C {
					rtcpSendErr := peerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(track.SSRC())}})
					if rtcpSendErr != nil {
						fmt.Println(rtcpSendErr)
					}
				}
			}()
		}

		if track.Kind() == webrtc.RTPCodecTypeAudio {
			codecName := strings.Split(track.Codec().RTPCodecCapability.MimeType, "/")[1]
			fmt.Printf("Track has started, of type %d: %s \n", track.PayloadType(), codecName)

			appSrc := receivePipelineForCodec(track, codecName)
			buf := make([]byte, 1400)
			for {
				i, _, readErr := track.Read(buf)
				if readErr != nil {
					panic(err)
				}

				appSrc.PushBuffer(gst.NewBufferFromBytes(buf[:i]))
			}
		}
	})

	// Set the remote SessionDescription
	err = peerConnection.SetRemoteDescription(*offer)
	if err != nil {
		panic(err)
	}

	// Create an answer
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		panic(err)
	}

	// Create channel that is blocked until ICE Gathering is complete
	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	// Sets the LocalDescription, and starts our UDP listeners
	err = peerConnection.SetLocalDescription(answer)
	if err != nil {
		panic(err)
	}

	// Block until ICE Gathering is complete, disabling trickle ICE
	// we do this because we only can exchange one signaling message
	// in a production application you should exchange ICE Candidates via OnICECandidate
	<-gatherComplete

	sendPipelineForCodec([]*webrtc.TrackLocalStaticSample{audioTrack}, audioSrc)

	return peerConnection.LocalDescription(), nil

}

func sendPipelineForCodec(tracks []*webrtc.TrackLocalStaticSample, pipelineSrc string) {
	pipelineStr := "appsink name=appsink"
	pipelineStr = pipelineSrc + " ! opusenc ! " + pipelineStr

	// switch codecName {
	// case "vp8":
	// 	pipelineStr = pipelineSrc + " ! vp8enc error-resilient=partitions keyframe-max-dist=10 auto-alt-ref=true cpu-used=5 deadline=1 ! " + pipelineStr
	// case "vp9":
	// 	pipelineStr = pipelineSrc + " ! vp9enc ! " + pipelineStr
	// case "h264":
	// 	pipelineStr = pipelineSrc + " ! video/x-raw,format=I420 ! x264enc speed-preset=ultrafast tune=zerolatency key-int-max=20 ! video/x-h264,stream-format=byte-stream ! " + pipelineStr
	// case "opus":
	// case "pcmu":
	// 	pipelineStr = pipelineSrc + " ! audio/x-raw, rate=8000 ! mulawenc ! " + pipelineStr
	// case "pcma":
	// 	pipelineStr = pipelineSrc + " ! audio/x-raw, rate=8000 ! alawenc ! " + pipelineStr
	// default:
	// 	panic("Unhandled codec " + codecName) //nolint
	// }

	pipeline, err := gst.NewPipelineFromString(pipelineStr)
	if err != nil {
		panic(err)
	}

	if err = pipeline.SetState(gst.StatePlaying); err != nil {
		panic(err)
	}

	appSink, err := pipeline.GetElementByName("appsink")
	if err != nil {
		panic(err)
	}

	app.SinkFromElement(appSink).SetCallbacks(&app.SinkCallbacks{
		NewSampleFunc: func(sink *app.Sink) gst.FlowReturn {
			sample := sink.PullSample()
			if sample == nil {
				return gst.FlowEOS
			}

			buffer := sample.GetBuffer()
			if buffer == nil {
				return gst.FlowError
			}

			samples := buffer.Map(gst.MapRead).Bytes()
			defer buffer.Unmap()

			for _, t := range tracks {
				if err := t.WriteSample(media.Sample{Data: samples, Duration: *buffer.Duration().AsDuration()}); err != nil {
					panic(err) //nolint
				}
			}

			return gst.FlowOK
		},
	})
}

// Create the appropriate GStreamer pipeline depending on what codec we are working with
func receivePipelineForCodec(track *webrtc.TrackRemote, codecName string) *app.Source {
	pipelineString := "appsrc format=time is-live=true do-timestamp=true name=src ! application/x-rtp"
	switch strings.ToLower(codecName) {
	case "vp8":
		pipelineString += fmt.Sprintf(", payload=%d, encoding-name=VP8-DRAFT-IETF-01 ! rtpvp8depay ! decodebin ! autovideosink", track.PayloadType())
	case "opus":
		pipelineString += fmt.Sprintf(", payload=%d, encoding-name=OPUS ! rtpopusdepay ! decodebin ! audioconvert ! audioresample ! alsasink device=hw:0,0", track.PayloadType())
	case "vp9":
		pipelineString += " ! rtpvp9depay ! decodebin ! autovideosink"
	case "h264":
		pipelineString += " ! rtph264depay ! decodebin ! autovideosink"
	case "g722":
		pipelineString += " clock-rate=8000 ! rtpg722depay ! decodebin ! autoaudiosink"
	default:
		panic("Unhandled codec " + codecName) //nolint
	}

	pipeline, err := gst.NewPipelineFromString(pipelineString)
	if err != nil {
		panic(err)
	}

	if err = pipeline.SetState(gst.StatePlaying); err != nil {
		panic(err)
	}

	appSrc, err := pipeline.GetElementByName("src")
	if err != nil {
		panic(err)
	}

	return app.SrcFromElement(appSrc)
}

func main() {

	// Initialize GStreamer
	gst.Init(nil)

	// Everything below is the Pion WebRTC API! Thanks for using it ❤️.

	// Block forever
	// select {}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.ServeFile(w, r, "index.html")
			return
		} else if r.Method == http.MethodPost {
			var offer webrtc.SessionDescription
			err := json.NewDecoder(r.Body).Decode(&offer)
			if err != nil {
				http.Error(w, "Invalid JSON", http.StatusBadRequest)
				return
			}

			answer, err := createPeerConnection(&offer)

			b, err := json.Marshal(answer)
			if err != nil {
				http.Error(w, fmt.Sprintf("failed to encode json response (%s)", err), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")

			w.Write(b)

		} else {
			http.Error(w, "", http.StatusNotFound)
		}
	})
	http.ListenAndServe(":8080", nil)

}
