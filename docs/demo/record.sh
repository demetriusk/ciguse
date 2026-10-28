#!/bin/sh
# Records docs/demo.gif and docs/demo.mp4: setup, logging, a two-week report on sample data,
# the status line.
# Needs vhs and ffmpeg. Run from the repository root: sh docs/demo/record.sh
set -eu

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/home"
go build -o "$work/bin/ciguse" .
export DEMO_BIN="$work/bin" DEMO_HOME="$work/home" # read by docs/demo/style.tape

# Scenes are recorded as GIFs: unlike mp4 they have no compression grain, which a zoom
# would magnify into changes on every frame.
vhs -q docs/demo/1-setup.tape -o "$work/1.gif"
vhs -q docs/demo/2-log.tape -o "$work/2.gif"
CIGUSE_HOME="$work/home/data" go run ./docs/demo/seed
vhs -q docs/demo/3-report.tape -o "$work/3.gif"
vhs -q docs/demo/4-status.tape -o "$work/4.gif"

# zoom FROM TO SCALE X Y: ease into SCALE around X,Y (pixels of the 1100x900 frame) from
# second FROM, and back out by TO. Ramps stay short: every ramp frame is a full new image.
zoom() {
	ramp=0.35
	z="st(0,in/25);st(1,min(clip((ld(0)-$1)/$ramp,0,1),clip(($2-ld(0))/$ramp,0,1)));1+($3-1)*ld(1)*ld(1)*(3-2*ld(1))"
	echo "scale=iw*3:ih*3:flags=lanczos,zoompan=z='$z':x='clip($4*3-iw/zoom/2,0,iw-iw/zoom)':y='clip($5*3-ih/zoom/2,0,ih-ih/zoom)':d=1:s=1100x900:fps=25"
}

scenes="
	[0:v]fps=25,format=rgb24[a];
	[1:v]fps=25,format=rgb24[b];
	[2:v]fps=25,$(zoom 5.0 10.4 1.6 360 321),format=rgb24[c];
	[3:v]fps=25,$(zoom 4.7 7.7 2.2 240 115),format=rgb24[d];
	[a][b][c][d]concat=n=4:v=1:a=0"

ffmpeg -v error -y -i "$work/1.gif" -i "$work/2.gif" -i "$work/3.gif" -i "$work/4.gif" -filter_complex "$scenes,fps=12,split[s][t];
	[s]palettegen=stats_mode=diff:max_colors=64[p];
	[t][p]paletteuse=dither=none:diff_mode=rectangle" docs/demo.gif
echo "wrote docs/demo.gif ($(du -h docs/demo.gif | cut -f1))"

# The same cut as video, for X and other sites that take video rather than GIFs: H.264 in
# yuv420p plays everywhere, at the full 25 fps and colors. It stays out of git (.gitignore).
ffmpeg -v error -y -i "$work/1.gif" -i "$work/2.gif" -i "$work/3.gif" -i "$work/4.gif" -filter_complex "$scenes,format=yuv420p" \
	-c:v libx264 -crf 18 -preset slow -tune animation -movflags +faststart docs/demo.mp4
echo "wrote docs/demo.mp4 ($(du -h docs/demo.mp4 | cut -f1))"
