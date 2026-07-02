#ifndef WIZCAMERA_CAMERA_H
#define WIZCAMERA_CAMERA_H

#include <stdbool.h>

// wizcameraAnyCameraOn reports whether any CoreMediaIO video device is
// currently running somewhere (in use by any process).
bool wizcameraAnyCameraOn(void);

// wizcameraStartListeners subscribes to CoreMediaIO property changes so
// goCameraChanged() is called whenever a camera starts or stops, or when
// devices appear or disappear. Safe to call once.
void wizcameraStartListeners(void);

#endif
