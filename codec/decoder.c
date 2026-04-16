#include "tetra-codec.h"
#include <stdio.h>

#define BYTES_PER_FRAME 18
#define SAMPLES_PER_FRAME 240

int main(int argc, char *argv[]) {
	if (argc < 3) {
		printf("usage: ./decoder encoded.bin output.raw\n");
		printf("input should be 4566 bit/s encoded audio\n");
		printf("output is signed 16-bit, mono, 8khz. (raw audio)\n");
		return -1;
	}

	int i = 0;

	short outbuf[SAMPLES_PER_FRAME];
	unsigned char inbuf[BYTES_PER_FRAME];

	FILE *fin = fopen(argv[1], "rb");
	FILE *fout = fopen(argv[2], "wb");

	tetra_codec *codec = tetra_decoder_create();

	printf("ETSI EN 300 395-2 V1.3.1 (2005-01)\n");
	printf("4566.66666 bit/s tetra codec decoder\n");
	printf("padded to 4800 bit/s\n\n");

	while(fread(inbuf, BYTES_PER_FRAME, 1, fin) == 1) {
		printf("frames decoded: %d\r", i);
		fflush(stdout);
		tetra_decode(codec, inbuf, outbuf, 0);
		fwrite(outbuf, 2, SAMPLES_PER_FRAME, fout);
		i++;
	}

	fclose(fin);
	fclose(fout);
	printf("\n\ndecoding complete.\n");
	return 0;
}
