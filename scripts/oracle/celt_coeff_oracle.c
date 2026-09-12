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
  int target, packet_target, decoder_size, frame, ret, packet_index;
  const unsigned char *cursor;
  const unsigned char *end;
  CELTDecoder *decoder;
  float pcm[960 * 2];

  if (argc < 2) {
    fprintf(stderr, "usage: %s vector.bit [constituent-frame] [packet-index]\n", argv[0]);
    return 2;
  }
  target = argc >= 3 ? atoi(argv[2]) : 0;
  packet_target = argc >= 4 ? atoi(argv[3]) : 0;
  if (target < 0 || packet_target < 0) return 2;
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

  decoder_size = celt_decoder_get_size(2);
  decoder = (CELTDecoder *)malloc((size_t)decoder_size);
  if (decoder == NULL || celt_decoder_init(decoder, 48000, 2) != OPUS_OK)
    return 2;
  celt_decoder_ctl(decoder, CELT_SET_SIGNALLING(0));

  cursor = data;
  end = data + file_size;
  for (packet_index = 0; packet_index <= packet_target; ++packet_index) {
    const unsigned char *packet;
    int config, endband, frame_count, frame_size, stream_channels;
    if (end - cursor < 8) return 2;
    packet_size = read_be32(cursor);
    if (packet_size < 2 || packet_size > (unsigned long)(end - cursor - 8))
      return 2;
    packet = cursor + 8;
    config = packet[0] >> 3;
    if (config < 16) return 2;
    frame_size = 120 << (config & 3);
    stream_channels = (packet[0] & 4) != 0 ? 2 : 1;
    switch ((config - 16) >> 2) {
      case 0: endband = 13; break;
      case 1: endband = 17; break;
      case 2: endband = 19; break;
      case 3: endband = 21; break;
      default: return 2;
    }
    if (celt_decoder_ctl(decoder, CELT_SET_END_BAND(endband)) != OPUS_OK ||
        celt_decoder_ctl(decoder, CELT_SET_CHANNELS(stream_channels)) != OPUS_OK)
      return 2;
    frame_count = split_frames(packet, (int)packet_size, frames, sizes);
    if (frame_count < 1) return 2;
    if (packet_index == packet_target && target >= frame_count) return 2;
    for (frame = 0; frame < frame_count; ++frame) {
      oracle_trace_enabled = packet_index == packet_target && frame == target;
      ret = celt_decode_with_ec(decoder, frames[frame], sizes[frame], pcm,
                                frame_size, NULL, 0);
      fprintf(stderr,
              "packet=%d frame=%d bytes=%d ret=%d traced=%d\n",
              packet_index, frame, sizes[frame], ret, oracle_trace_enabled);
      if (ret < 0) return 1;
    }
    cursor += 8 + packet_size;
  }
  oracle_trace_enabled = 0;
  free(decoder);
  free(data);
  return 0;
}
