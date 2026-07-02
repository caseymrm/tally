#ifndef TALLY_CAMERA_H
#define TALLY_CAMERA_H

#include <stdbool.h>

// tallyAnyCameraOn reports whether any CoreMediaIO video device is
// currently running somewhere (in use by any process).
bool tallyAnyCameraOn(void);

// tallyStartListeners subscribes to CoreMediaIO property changes so
// goCameraChanged() is called whenever a camera starts or stops, or when
// devices appear or disappear. Safe to call once.
void tallyStartListeners(void);

#endif
