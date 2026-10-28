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
# The color information is in both the HEVC stream and the MOV file.
ffmpeg -v error -y -f lavfi -i "$pattern:d=2" \
	-vf "setparams=color_primaries=bt2020:color_trc=arib-std-b67:colorspace=bt2020nc" \
	$x265:keyint=10:min-keyint=10:scenecut=0:colorprim=bt2020:transfer=arib-std-b67:colormatrix=bt2020nc \
	-pix_fmt yuv420p10le hdr.mov

# HEVC in Display P3, like iPhone Live Photo videos, of the sRGB color
# #996633. Like iPhones, it says it has BT.709's transfer function, but
# has sRGB's.
ffmpeg -v error -y -f lavfi -i "color=c=0x996633:s=64x48:r=10:d=0.2,format=gbrpf32le" \
	-vf "zscale=tin=iec61966-2-1:pin=709:min=gbr:rin=full:t=iec61966-2-1:p=smpte432:m=170m:r=full,format=yuvj420p,setparams=color_trc=bt709" \
	$x265:colorprim=smpte432:transfer=bt709:colormatrix=smpte170m:range=full p3.mov

# HEVC with only its first frame a keyframe.
ffmpeg -v error -y -f lavfi -i "$pattern:d=2" $x265:keyint=1000:min-keyint=1000:scenecut=0 -pix_fmt yuv420p onekey.mov

# H.264, which is decoded with ffmpeg.
ffmpeg -v error -y -f lavfi -i "$pattern:d=1" -c:v libx264 -pix_fmt yuv420p h264.mp4

# H.264 with keyframes every 0.5 seconds, rotated 90 degrees clockwise.
ffmpeg -v error -y -f lavfi -i "$pattern:d=2" -c:v libx264 -g 5 -pix_fmt yuv420p h264-unrotated.mp4
ffmpeg -v error -y -display_rotation:v -90 -i h264-unrotated.mp4 -c copy h264-rotated.mp4
rm h264-unrotated.mp4

# H.264 in BT.709's limited range of the sRGB color #996633, like
# p3.mov.
ffmpeg -v error -y -f lavfi -i "color=c=0x996633:s=64x48:r=10:d=0.2,format=gbrpf32le" \
	-vf "zscale=tin=iec61966-2-1:pin=709:min=gbr:rin=full:t=iec61966-2-1:p=709:m=709:r=limited,format=yuv420p,setparams=color_trc=bt709" \
	-c:v libx264 sienna709.mp4

# Other codecs of older phones and cameras, decoded with ffmpeg: H.263
# and MPEG-4 Part 2 keyframes, and Motion JPEG in QuickTime.
ffmpeg -v error -y -f lavfi -i "testsrc=s=128x96:r=10:d=2" -c:v h263 h263.3gp
ffmpeg -v error -y -f lavfi -i "$pattern:d=2" -c:v mpeg4 mpeg4.mp4
ffmpeg -v error -y -f lavfi -i "$pattern:d=2" -c:v mjpeg -pix_fmt yuvj420p mjpeg.mov

# Other formats, which ffmpeg reads whole.
ffmpeg -v error -y -f lavfi -i "$pattern:d=2" -c:v wmv2 wmv2.wmv
ffmpeg -v error -y -f lavfi -i "$pattern:d=2" -c:v libvpx vp8.webm

# Motion JPEG in an AVI, as from older cameras.
ffmpeg -v error -y -f lavfi -i "$pattern:d=2" -c:v mjpeg -pix_fmt yuvj420p mjpeg.avi
