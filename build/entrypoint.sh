#!/bin/sh
set -e

ARGS=""

[ -n "$RELAY_URL" ] && ARGS="$ARGS -relay $RELAY_URL"
[ -n "$TOKEN" ]     && ARGS="$ARGS -token $TOKEN"
[ -n "$OBS_HOST" ]  && ARGS="$ARGS -obs-host $OBS_HOST"
[ -n "$OBS_PORT" ]  && ARGS="$ARGS -obs-port $OBS_PORT"
[ -n "$OBS_PASS" ]  && ARGS="$ARGS -obs-pass $OBS_PASS"

exec obs-agent $ARGS "$@"
