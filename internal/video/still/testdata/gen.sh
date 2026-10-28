#!/bin/sh
# Generates this directory's test videos with ffmpeg (7.1, with libx265
# and libx264). They're 64x48 at 10 frames per second.
set -e
cd "$(dirname "$0")"

x265="-c:v libx265 -tag:v hvc1 -x265-params log-level=error"
pattern="testsrc=s=64x48:r=10"

# HEVC, black for 1.2 seconds and then a test pattern, with keyframes
# every 0.5 seconds, rotated 90 degrees clockwise for display, like
# iPhone portrait videos (display_rotation is counterclockwise).
# (ffmpeg 7.1 crashes setting the rotation of a filter graph's stream,
# so it's set while copying the encoded stream.)
ffmpeg -v error -y -f lavfi -i "color=black:s=64x48:r=10:d=1.2[a];$pattern:d=1.8[b];[a][b]concat" \
	$x265:keyint=5:min-keyint=5:scenecut=0 -pix_fmt yuv420p fade-unrotated.mov
ffmpeg -v error -y -display_rotation:v -90 -i fade-unrotated.mov -c copy fade.mov
rm fade-unrotated.mov

# 10-bit HLG HEVC, like iPhone HDR videos, with one keyframe a second.
ffmpeg -v error -y -f lavfi -i "$pattern:d=2" \
	$x265:keyint=10:min-keyint=10:scenecut=0:colorprim=bt2020:transfer=arib-std-b67:colormatrix=bt2020nc \
	-pix_fmt yuv420p10le hdr.mov

# HEVC with only its first frame a keyframe.
ffmpeg -v error -y -f lavfi -i "$pattern:d=2" $x265:keyint=1000:min-keyint=1000:scenecut=0 -pix_fmt yuv420p onekey.mov

# H.264, which isn't supported.
ffmpeg -v error -y -f lavfi -i "$pattern:d=1" -c:v libx264 -pix_fmt yuv420p h264.mp4

# Motion JPEG in an AVI, as from older cameras.
ffmpeg -v error -y -f lavfi -i "$pattern:d=2" -c:v mjpeg -pix_fmt yuvj420p mjpeg.avi
