#!/bin/sh
set -e

ARGS=""

[ -n "$TOKEN" ]    && ARGS="$ARGS -token $TOKEN"
[ -n "$OBS_PASS" ] && ARGS="$ARGS -obs-pass $OBS_PASS"
[ -n "$OBS_PORT" ] && ARGS="$ARGS -obs-port $OBS_PORT"

exec obs-agent $ARGS "$@"
