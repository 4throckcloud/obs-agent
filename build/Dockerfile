FROM alpine:3.20

ARG VERSION=dev
ARG TARGETARCH

LABEL org.opencontainers.image.source="https://github.com/4throckcloud/obs-agent"
LABEL org.opencontainers.image.version="${VERSION}"
LABEL org.opencontainers.image.description="OBS Agent — headless relay for OBS WebSocket"

RUN apk add --no-cache ca-certificates

COPY obs-agent-linux-${TARGETARCH} /usr/local/bin/obs-agent
RUN chmod +x /usr/local/bin/obs-agent

ENV TOKEN="" \
    OBS_PASS="" \
    OBS_PORT="" \
    OBS_HOST="host.docker.internal"

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
