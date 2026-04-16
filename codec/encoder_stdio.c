/*
 * tetra-acelp stdio streaming encoder
 * Reads raw PCM s16le mono 8kHz from stdin,
 * writes 18-byte packed ACELP frames to stdout.
 * Keeps codec state between frames.
 *
 * Build: gcc -Icodec/ -Ofast encoder_stdio.c codec/*.c -o tetra-acelp-stdio
 */
#include "tetra-codec.h"
#include <stdio.h>
#include <signal.h>

#define BYTES_PER_FRAME 18
#define SAMPLES_PER_FRAME 240

static volatile int running = 1;
static void handle_signal(int sig) { running = 0; }

int main(void) {
    signal(SIGPIPE, handle_signal);
    signal(SIGINT, handle_signal);

    short inbuf[SAMPLES_PER_FRAME];
    unsigned char encbuf[BYTES_PER_FRAME];

    tetra_codec *codec = tetra_encoder_create();

    fprintf(stderr, "tetra-acelp-stdio: streaming encoder ready (480B PCM -> 18B ACELP)\n");

    while (running) {
        if (fread(inbuf, 2, SAMPLES_PER_FRAME, stdin) != SAMPLES_PER_FRAME)
            break;

        tetra_encode(codec, inbuf, encbuf);

        if (fwrite(encbuf, BYTES_PER_FRAME, 1, stdout) != 1)
            break;
        fflush(stdout);
    }

    return 0;
}
