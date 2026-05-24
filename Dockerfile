FROM golang:1.25-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO required for github.com/uber/h3-go/v4 (coverage-map H3 indexing).
# Static linking via -extldflags=-static keeps the binaries portable enough
# for the slim runtime image.
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags '-extldflags "-static"' -o /out/tetra-brew ./cmd/tetra-brew
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags '-extldflags "-static"' -o /out/tetra-brew-webradio ./cmd/tetra-brew-webradio
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags '-extldflags "-static"' -o /out/tetra-brew-echo ./cmd/tetra-brew-echo
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags '-extldflags "-static"' -o /out/tetra-brew-proxy ./cmd/tetra-brew-proxy

# Build ACELP encoder/decoder from included source.
# Only the non-main sources go in; encoder.c/encoder_stdio.c/decoder.c each
# have their own main() and are linked individually.
RUN gcc -Icodec/ -Ofast codec/encoder_stdio.c codec/tetra-codec.c codec/tetra-codec-impl.c -o /out/tetra-acelp-stdio
RUN gcc -Icodec/ -Ofast codec/decoder.c codec/tetra-codec.c codec/tetra-codec-impl.c -o /out/tetra-acelp-decoder

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates ffmpeg && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=build /out/tetra-brew /app/tetra-brew
COPY --from=build /out/tetra-brew-webradio /app/tetra-brew-webradio
COPY --from=build /out/tetra-brew-echo /app/tetra-brew-echo
COPY --from=build /out/tetra-brew-proxy /app/tetra-brew-proxy
COPY --from=build /out/tetra-acelp-stdio /app/tetra-acelp-stdio
COPY --from=build /out/tetra-acelp-decoder /app/tetra-acelp-decoder

ENV WEBRADIO_ENCODER_BIN=/app/tetra-acelp-stdio
ENV WEBRADIO_FFMPEG_BIN=ffmpeg

EXPOSE 8080
ENTRYPOINT ["/app/tetra-brew"]
