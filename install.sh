#!/bin/sh
# Installs the latest lopper release for macOS or Linux:
#
#   curl -fsSL https://raw.githubusercontent.com/shinokamix/lopper/main/install.sh | sh
#
# LOPPER_VERSION=v0.1.0 installs that release instead; LOPPER_INSTALL_DIR
# (default ~/.local/bin) is where the binary goes. The archive is checked
# against the release's checksums.txt before anything is installed.
set -eu

repo="https://github.com/shinokamix/lopper"
dir="${LOPPER_INSTALL_DIR:-$HOME/.local/bin}"

fail() {
	echo "lopper install: $*" >&2
	exit 1
}

case "$(uname -s)" in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) fail "unsupported system $(uname -s); download a release from $repo/releases" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) fail "unsupported architecture $(uname -m); download a release from $repo/releases" ;;
esac

if [ -n "${LOPPER_VERSION:-}" ]; then
	# With or without the v of the tag: 0.1.0 is v0.1.0.
	tag="v${LOPPER_VERSION#v}"
else
	# releases/latest redirects to releases/tag/<latest tag>.
	latest=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$repo/releases/latest") ||
		fail "cannot reach $repo"
	tag="${latest##*/}"
	case "$tag" in
	v*) ;;
	*) fail "no release found at $repo/releases" ;;
	esac
fi

if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
	fail "sha256sum or shasum is needed to verify the download"
fi

stage=""
tmp=$(mktemp -d)
trap 'rm -rf "$tmp" ${stage:+"$stage"}' EXIT
# dash runs the EXIT trap on exit only, not when a signal ends the script.
trap 'exit 1' HUP INT TERM

archive="lopper_${os}_${arch}.tar.gz"
echo "Downloading lopper $tag for $os/$arch"
curl -fsSL -o "$tmp/$archive" "$repo/releases/download/$tag/$archive" ||
	fail "cannot download $archive of $tag"
curl -fsSL -o "$tmp/checksums.txt" "$repo/releases/download/$tag/checksums.txt" ||
	fail "cannot download checksums.txt of $tag"

want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || fail "$archive is not listed in checksums.txt"
[ "$(sha256 "$tmp/$archive")" = "$want" ] || fail "$archive does not match its checksum"

tar -xzf "$tmp/$archive" -C "$tmp" lopper
mkdir -p "$dir"
# Copied to a file of its own beside the target, then renamed over it: a
# running lopper keeps its file, two installs at once never write the same
# one, and a copy cut short is removed on exit.
stage=$(mktemp "$dir/.lopper.XXXXXX")
cp "$tmp/lopper" "$stage"
chmod 755 "$stage"
# Run before it replaces anything: $dir may be mounted noexec.
"$stage" --version >/dev/null || fail "the downloaded lopper does not run"
mv -f "$stage" "$dir/lopper"
echo "Installed lopper $tag to $dir/lopper"

case ":$PATH:" in
*":$dir:"*) ;;
*) echo "$dir is not in your PATH; add it to run lopper by name" ;;
esac
# A lopper installed some other way would still run instead.
found=$(command -v lopper || true)
if [ -n "$found" ] && [ "$found" != "$dir/lopper" ]; then
	echo "$found comes first in your PATH: lopper runs it, not $dir/lopper"
fi
