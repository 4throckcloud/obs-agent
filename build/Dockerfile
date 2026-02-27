FROM alpine:3.20

ARG VERSION=dev

LABEL org.opencontainers.image.source="https://github.com/4throckcloud/obs-agent"
LABEL org.opencontainers.image.version="${VERSION}"
LABEL org.opencontainers.image.description="OBS Agent — headless relay for OBS WebSocket"

RUN apk add --no-cache ca-certificates

COPY obs-agent-linux-amd64 /usr/local/bin/obs-agent
RUN chmod +x /usr/local/bin/obs-agent

ENV TOKEN="" \
    OBS_PASS=""

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
