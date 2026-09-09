/* Decode one CELT packet constituent-by-constituent for coefficient tracing. */
#include <stdio.h>
#include <stdlib.h>

#include "celt.h"

int oracle_trace_enabled = 0;

static unsigned int read_be32(const unsigned char *p) {
  return ((unsigned int)p[0] << 24) | ((unsigned int)p[1] << 16) |
         ((unsigned int)p[2] << 8) | p[3];
}

static int parse_size(const unsigned char **cursor, const unsigned char *end) {
  int first;
  if (*cursor >= end) return -1;
  first = *(*cursor)++;
  if (first < 252) return first;
  if (*cursor >= end) return -1;
  return first + 4 * *(*cursor)++;
}

static int split_frames(const unsigned char *packet, int packet_len,
                        const unsigned char **frames, int *sizes) {
  int code = packet[0] & 3;
  const unsigned char *cursor = packet + 1;
  const unsigned char *end = packet + packet_len;
  int count, i, padding = 0, total = 0;
  if (code == 0) {
    frames[0] = cursor;
    sizes[0] = (int)(end - cursor);
    return 1;
  }
  if (code == 1) {
    int size = (int)(end - cursor) / 2;
    if (2 * size != end - cursor) return -1;
    frames[0] = cursor;
    frames[1] = cursor + size;
    sizes[0] = sizes[1] = size;
    return 2;
  }
  if (code == 2) {
    sizes[0] = parse_size(&cursor, end);
    if (sizes[0] < 0 || cursor + sizes[0] > end) return -1;
    frames[0] = cursor;
    frames[1] = cursor + sizes[0];
    sizes[1] = (int)(end - frames[1]);
    return 2;
  }

  if (cursor >= end) return -1;
  {
    int control = *cursor++;
    int vbr = (control & 0x80) != 0;
    count = control & 0x3f;
    if (count < 1 || count > 48) return -1;
    if (control & 0x40) {
      int value;
      do {
        if (cursor >= end) return -1;
        value = *cursor++;
        padding += value == 255 ? 254 : value;
      } while (value == 255);
    }
    if (padding > end - cursor) return -1;
    end -= padding;
    if (vbr) {
      for (i = 0; i < count - 1; ++i) {
        sizes[i] = parse_size(&cursor, end);
        if (sizes[i] < 0) return -1;
        total += sizes[i];
      }
      sizes[count - 1] = (int)(end - cursor) - total;
    } else {
      int bytes = (int)(end - cursor);
      if (bytes % count != 0) return -1;
      for (i = 0; i < count; ++i) sizes[i] = bytes / count;
    }
    for (i = 0; i < count; ++i) {
      if (sizes[i] < 0 || cursor + sizes[i] > end) return -1;
      frames[i] = cursor;
      cursor += sizes[i];
    }
  }
  return count;
}

int main(int argc, char **argv) {
  FILE *file;
  long file_size;
  unsigned char *data;
  unsigned int packet_size;
  const unsigned char *frames[48];
  int sizes[48];
  int frame_count, target, decoder_size, frame, ret;
  CELTDecoder *decoder;
  float pcm[960 * 2];

  if (argc < 2) {
    fprintf(stderr, "usage: %s vector.bit [constituent-frame]\n", argv[0]);
    return 2;
  }
  target = argc >= 3 ? atoi(argv[2]) : 0;
  file = fopen(argv[1], "rb");
  if (file == NULL) return 2;
  fseek(file, 0, SEEK_END);
  file_size = ftell(file);
  fseek(file, 0, SEEK_SET);
  data = (unsigned char *)malloc((size_t)file_size);
  if (data == NULL || fread(data, 1, (size_t)file_size, file) !=
                          (size_t)file_size) return 2;
  fclose(file);
  if (file_size < 9) return 2;
  packet_size = read_be32(data);
  if (packet_size + 8 > (unsigned long)file_size) return 2;
  frame_count = split_frames(data + 8, (int)packet_size, frames, sizes);
  if (frame_count < 1 || target < 0 || target >= frame_count) return 2;

  decoder_size = celt_decoder_get_size(2);
  decoder = (CELTDecoder *)malloc((size_t)decoder_size);
  if (decoder == NULL || celt_decoder_init(decoder, 48000, 2) != OPUS_OK)
    return 2;
  celt_decoder_ctl(decoder, CELT_SET_SIGNALLING(0));
  for (frame = 0; frame < frame_count; ++frame) {
    oracle_trace_enabled = frame == target;
    ret = celt_decode_with_ec(decoder, frames[frame], sizes[frame], pcm, 960,
                              NULL, 0);
    fprintf(stderr, "frame=%d bytes=%d ret=%d traced=%d\n", frame,
            sizes[frame], ret, oracle_trace_enabled);
    if (ret < 0) return 1;
  }
  oracle_trace_enabled = 0;
  free(decoder);
  free(data);
  return 0;
}
