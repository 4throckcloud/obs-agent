#!/bin/sh
set -e

ARGS="-obs-host host.docker.internal -obs-port 4455"

[ -n "$TOKEN" ]    && ARGS="$ARGS -token $TOKEN"
[ -n "$OBS_PASS" ] && ARGS="$ARGS -obs-pass $OBS_PASS"

exec obs-agent $ARGS "$@"
