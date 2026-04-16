FROM golang:1.24-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/tetra-brew ./cmd/tetra-brew
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/tetra-brew-webradio ./cmd/tetra-brew-webradio
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/tetra-brew-echo ./cmd/tetra-brew-echo

# Build ACELP encoder/decoder from included source
RUN gcc -Icodec/ -Ofast codec/encoder_stdio.c codec/codec/*.c -o /out/tetra-acelp-stdio || echo "ACELP encoder build skipped"
RUN gcc -Icodec/ -Ofast codec/decoder.c codec/codec/*.c -o /out/tetra-acelp-decoder || echo "ACELP decoder build skipped"

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates ffmpeg && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=build /out/tetra-brew /app/tetra-brew
COPY --from=build /out/tetra-brew-webradio /app/tetra-brew-webradio
COPY --from=build /out/tetra-brew-echo /app/tetra-brew-echo
COPY --from=build /out/tetra-acelp-stdio /app/tetra-acelp-stdio
COPY --from=build /out/tetra-acelp-decoder /app/tetra-acelp-decoder

ENV WEBRADIO_ENCODER_BIN=/app/tetra-acelp-stdio
ENV WEBRADIO_FFMPEG_BIN=ffmpeg

EXPOSE 8080
ENTRYPOINT ["/app/tetra-brew"]
