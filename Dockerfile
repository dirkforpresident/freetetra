FROM golang:1.24-bookworm AS build

WORKDIR /src

RUN apt-get update && apt-get install -y --no-install-recommends gcc ca-certificates && rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
COPY _other_repos_/go-zello-client ./_other_repos_/go-zello-client
RUN go mod download

COPY . .

RUN if [ -d "_other_repos_/tetra-acelp" ]; then (cd _other_repos_/tetra-acelp && sh build-fast.sh); fi
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/tetra-brew ./cmd/tetra-brew
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/tetra-brew-webradio ./cmd/tetra-brew-webradio
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/tetra-brew-zello ./cmd/tetra-brew-zello
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/tetra-brew-echo ./cmd/tetra-brew-echo

FROM debian:bookworm-slim AS runtime

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates ffmpeg && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=build /out/tetra-brew /app/bin/tetra-brew
COPY --from=build /out/tetra-brew-webradio /app/bin/tetra-brew-webradio
COPY --from=build /out/tetra-brew-zello /app/bin/tetra-brew-zello
COPY --from=build /out/tetra-brew-echo /app/bin/tetra-brew-echo
COPY --from=build /src/_other_repos_/tetra-acelp/tetra-acelp-stdio /app/bin/tetra-acelp-stdio
COPY --from=build /src/_other_repos_/tetra-acelp/tetra-acelp-stdio-decoder /app/bin/tetra-acelp-stdio-decoder

ENV WEBRADIO_ENCODER_BIN=/app/bin/tetra-acelp-stdio
ENV ZELLO_TRAFFIC_ENCODER_BIN=/app/bin/tetra-acelp-stdio
ENV ZELLO_TRAFFIC_DECODER_BIN=/app/bin/tetra-acelp-stdio-decoder
ENV WEBRADIO_FFMPEG_BIN=ffmpeg
ENV ZELLO_TRAFFIC_FFMPEG_BIN=ffmpeg

ENTRYPOINT ["/app/bin/tetra-brew"]
