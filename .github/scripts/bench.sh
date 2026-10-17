#!/bin/sh
# Times `lopper scan` built from a base commit and from this checkout on the
# same generated repositories, and fails when a scan is slower by more than
# BENCH_MAX_SLOWDOWN percent. The two builds take turns, so a noisy runner
# slows both alike.
set -eu
base=${1:-main}
rounds=${BENCH_ROUNDS:-10}
max_slowdown=${BENCH_MAX_SLOWDOWN:-20}

root=$(git rev-parse --show-toplevel)
cd "$root/apps/lopper"
set -- cmd internal go.mod go.sum "$root/.github/scripts/bench.sh"
if git diff --quiet "$base" -- "$@" && [ -z "$(git ls-files --others --exclude-standard -- "$@")" ]; then
	echo "lopper is the same as in $base: nothing to compare"
	exit 0
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/base"
git -C "$root" archive "$base" apps/lopper | tar -x -C "$work/base"
(cd "$work/base/apps/lopper" && go build -o "$work/lopper-base" ./cmd/lopper)
go build -o "$work/lopper-head" ./cmd/lopper
go test -c -o "$work/bench.test" ./cmd/lopper

export LOPPER_BENCH_FIXTURE="$work/fixture"
bench() {
	LOPPER_BENCH_BIN="$work/lopper-$1" "$work/bench.test" -test.run '^$' -test.bench . -test.benchtime 1x
}
# The first scans fill the file system caches and are not counted.
bench base >/dev/null
bench head >/dev/null
i=0
while [ "$i" -lt "$rounds" ]; do
	bench base >>"$work/base.txt"
	bench head >>"$work/head.txt"
	i=$((i + 1))
done

benchstat() {
	go tool -modfile tools/go.mod benchstat "$@" base="$work/base.txt" head="$work/head.txt"
}
benchstat | tee "$work/table.txt"

# benchstat prints a change only when it is significant, and "~" otherwise.
slower=$(benchstat -format csv 2>/dev/null | awk -F, -v max="$max_slowdown" '
	$1 == "" && $2 != "" && $2 !~ /^(base|head)$/ { unit = $2 }
	unit == "sec/op" && $1 ~ /^Scan\// && $6 ~ /^\+[0-9.]+%$/ && $6 + 0 > max { print $1 " " $6 }
')
if [ -n "$slower" ]; then
	verdict=":x: Slower than base by more than $max_slowdown%: $(echo "$slower" | paste -sd , - | sed "s/,/, /g")"
else
	verdict=":white_check_mark: No scan is slower than base by more than $max_slowdown%."
fi

# BENCH_REPORT receives the result as Markdown, for a pull request comment.
report=${BENCH_REPORT:-$work/report.md}
{
	printf '### Scan speed\n\n%s\n\n' "$verdict"
	printf '`base` is %s, `head` is this change.\n\n' "$(git rev-parse --short "$base")"
	printf '```\n%s\n```\n' "$(cat "$work/table.txt")"
} >"$report"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
	cat "$report" >>"$GITHUB_STEP_SUMMARY"
fi

if [ -n "$slower" ]; then
	echo "slower than $base by more than $max_slowdown%:"
	echo "$slower"
	exit 1
fi
