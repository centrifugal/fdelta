/*
** Wrappers that let the Go tests drive Fossil's reference implementation.
**
** fossil/delta.c is included rather than compiled on its own so that this file
** can reach checksum(), which is static there. Keeping it in a subdirectory
** also stops cgo from compiling it a second time as a translation unit of its
** own. Nothing in fossil/delta.c is modified.
**
** FOSSIL_ENABLE_DELTA_CKSUM_TEST makes delta_apply verify the checksum a delta
** carries. Fossil leaves that off by default because it checks content hashes
** at a higher level; this package always verifies, so the reference is
** configured to match before the two are compared.
*/
#define FOSSIL_ENABLE_DELTA_CKSUM_TEST 1

#include "fossil/delta.c"

unsigned int fdelta_ref_checksum(const char *z, unsigned int n) {
  return checksum(z, (size_t)n);
}

int fdelta_ref_digit_count(int v) {
  return digit_count(v);
}

unsigned int fdelta_ref_hash_once(const char *z) {
  return hash_once(z);
}
