#include "tetra-codec.h"
#include <stdio.h>

#define BYTES_PER_FRAME 18
#define SAMPLES_PER_FRAME 240

int main(int argc, char *argv[]) {
	if (argc < 3) {
		printf("usage: ./encoder input.raw encoded.bin\n");
		printf("input should be signed 16-bit, mono, 8khz. (raw audio)\n");
		printf("output is 4566 bit/s encoded audio\n");
		return -1;
	}

	int i = 0;

	short inbuf[SAMPLES_PER_FRAME];
	unsigned char encbuf[BYTES_PER_FRAME];

	FILE *fin = fopen(argv[1], "rb");
	FILE *fout = fopen(argv[2], "wb");

	tetra_codec *codec = tetra_encoder_create();

	printf("ETSI EN 300 395-2 V1.3.1 (2005-01)\n");
	printf("4566.66666 bit/s tetra codec encoder\n");
	printf("padded to 4800 bit/s\n\n");

	while(fread(inbuf, 2, SAMPLES_PER_FRAME, fin) == SAMPLES_PER_FRAME) {
		printf("frames encoded: %d\r", i);
		fflush(stdout);
		tetra_encode(codec, inbuf, encbuf);
		fwrite(encbuf, BYTES_PER_FRAME, 1, fout);
		i++;
	}

	fclose(fin);
	fclose(fout);
	printf("\n\nencoding complete.\n");
	return 0;
}
