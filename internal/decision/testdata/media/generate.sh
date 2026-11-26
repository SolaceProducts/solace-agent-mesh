#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"

tone=(-f lavfi -i "sine=frequency=440:sample_rate=8000:duration=0.1")
frame=(-f lavfi -i "testsrc=size=16x16:rate=1:duration=1")
ff=(ffmpeg -hide_banner -loglevel error -y)

"${ff[@]}" "${tone[@]}" -ac 1 tone.wav
"${ff[@]}" "${tone[@]}" -ac 1 -c:a libmp3lame -b:a 8k tone.mp3
"${ff[@]}" "${tone[@]}" -ac 1 -c:a libmp3lame -b:a 8k -id3v2_version 0 -write_xing 0 tone_no_id3.mp3
"${ff[@]}" "${tone[@]}" -ac 1 tone.flac
"${ff[@]}" "${tone[@]}" -ac 1 -c:a aac -b:a 8k tone.m4a
"${ff[@]}" "${tone[@]}" -ac 1 -c:a aac -b:a 8k -f mp4 tone_audio.mp4
"${ff[@]}" "${tone[@]}" -ac 1 -c:a aac -b:a 8k -f adts tone.aac
"${ff[@]}" "${tone[@]}" -ac 2 -c:a vorbis -strict -2 tone.ogg
"${ff[@]}" "${tone[@]}" -ac 1 -ar 48000 -c:a libopus -b:a 8k tone.opus
"${ff[@]}" "${tone[@]}" -ac 1 tone.aiff
"${ff[@]}" "${tone[@]}" -ac 1 -ar 48000 -c:a libopus -b:a 8k -f webm tone.weba

"${ff[@]}" "${frame[@]}" -frames:v 1 -pix_fmt yuv420p clip.mp4
"${ff[@]}" "${frame[@]}" -frames:v 1 -pix_fmt yuv420p -f ipod clip.m4v
"${ff[@]}" "${frame[@]}" -frames:v 1 -pix_fmt yuv420p clip.mov
"${ff[@]}" "${frame[@]}" -frames:v 1 -c:v mpeg4 clip.avi
"${ff[@]}" "${frame[@]}" -frames:v 1 -c:v libvpx -b:v 50k clip.webm
"${ff[@]}" "${frame[@]}" -frames:v 1 -c:v wmv2 clip.wmv
"${ff[@]}" "${frame[@]}" -frames:v 1 -c:v flv1 clip.flv
"${ff[@]}" "${frame[@]}" -frames:v 1 -s 128x96 -c:v h263 clip.3gp
"${ff[@]}" "${frame[@]}" -frames:v 1 -r 25 -c:v mpeg1video -f mpeg clip.mpg

magick -size 16x16 pattern:checkerboard image.png
magick image.png image.gif
magick image.png BMP3:image.bmp
magick image.png -depth 8 -type TrueColor -compress none image.tiff
magick image.png -compress none image_4bit.tiff
heif-enc -q 50 -o image.heic image.png >/dev/null
heif-enc -q 50 -A -o image.avif image.png >/dev/null
magick -size 32x32 xc:white document.pdf
