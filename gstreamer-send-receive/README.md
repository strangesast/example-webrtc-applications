# gstreamer-send-receive
gstreamer-send-receive is a simple application that shows how to receive media using Pion WebRTC and play live using GStreamer.

## Instructions
### Install GStreamer
This example requires you have GStreamer installed, these are the supported platforms
#### Debian/Ubuntu
`sudo apt-get install libgstreamer1.0-dev libgstreamer-plugins-base1.0-dev gstreamer1.0-plugins-good`
#### Windows MinGW64/MSYS2
`pacman -S mingw-w64-x86_64-gstreamer mingw-w64-x86_64-gst-libav mingw-w64-x86_64-gst-plugins-good mingw-w64-x86_64-gst-plugins-bad mingw-w64-x86_64-gst-plugins-ugly`
#### macOS
` brew install gst-plugins-good pkg-config && export PKG_CONFIG_PATH="/usr/local/opt/libffi/lib/pkgconfig"`

### Run gstreamer-send-receive
#### Linux/macOS/Windows
Run `gstreamer-send-receive`

### Hit 'Start Session' in jsfiddle, enjoy your media!
Your video and/or audio should popup automatically, and will continue playing until you close the application.
