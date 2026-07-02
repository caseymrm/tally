#import <CoreMediaIO/CMIOHardware.h>
#import <dispatch/dispatch.h>
#include <stdlib.h>
#include "camera.h"

// Implemented in Go (camera.go).
extern void goCameraChanged(void);

static CMIOObjectPropertyAddress devicesAddress = {
	kCMIOHardwarePropertyDevices,
	kCMIOObjectPropertyScopeGlobal,
	kCMIOObjectPropertyElementMain,
};

static CMIOObjectPropertyAddress runningAddress = {
	kCMIODevicePropertyDeviceIsRunningSomewhere,
	kCMIOObjectPropertyScopeWildcard,
	kCMIOObjectPropertyElementWildcard,
};

static dispatch_queue_t listenerQueue(void) {
	static dispatch_queue_t queue = NULL;
	static dispatch_once_t once;
	dispatch_once(&once, ^{
		queue = dispatch_queue_create("wizcamera.cmio", DISPATCH_QUEUE_SERIAL);
	});
	return queue;
}

// copyDeviceIDs returns a malloc'd array of CMIO device IDs in *outIDs and
// the device count. Caller frees.
static UInt32 copyDeviceIDs(CMIOObjectID **outIDs) {
	*outIDs = NULL;
	UInt32 dataSize = 0;
	if (CMIOObjectGetPropertyDataSize(kCMIOObjectSystemObject, &devicesAddress,
	                                  0, NULL, &dataSize) != kCMIOHardwareNoError) {
		return 0;
	}
	UInt32 count = dataSize / sizeof(CMIOObjectID);
	if (count == 0) {
		return 0;
	}
	CMIOObjectID *ids = malloc(dataSize);
	UInt32 dataUsed = 0;
	if (CMIOObjectGetPropertyData(kCMIOObjectSystemObject, &devicesAddress,
	                              0, NULL, dataSize, &dataUsed, ids) != kCMIOHardwareNoError) {
		free(ids);
		return 0;
	}
	*outIDs = ids;
	return count;
}

static bool deviceIsRunningSomewhere(CMIOObjectID device) {
	if (!CMIOObjectHasProperty(device, &runningAddress)) {
		return false;
	}
	UInt32 running = 0;
	UInt32 dataUsed = 0;
	if (CMIOObjectGetPropertyData(device, &runningAddress, 0, NULL,
	                              sizeof(running), &dataUsed, &running) != kCMIOHardwareNoError) {
		return false;
	}
	return running != 0;
}

bool wizcameraAnyCameraOn(void) {
	CMIOObjectID *ids = NULL;
	UInt32 count = copyDeviceIDs(&ids);
	bool on = false;
	for (UInt32 i = 0; i < count; i++) {
		if (deviceIsRunningSomewhere(ids[i])) {
			on = true;
			break;
		}
	}
	free(ids);
	return on;
}

#define MAX_LISTENED_DEVICES 128
static CMIOObjectID listenedDevices[MAX_LISTENED_DEVICES];
static int listenedCount = 0;

static bool alreadyListening(CMIOObjectID device) {
	for (int i = 0; i < listenedCount; i++) {
		if (listenedDevices[i] == device) {
			return true;
		}
	}
	return false;
}

// registerDeviceListeners subscribes to the running state of any device we
// haven't seen yet. Called from listenerQueue (serial), so no locking.
static void registerDeviceListeners(void) {
	CMIOObjectID *ids = NULL;
	UInt32 count = copyDeviceIDs(&ids);
	for (UInt32 i = 0; i < count; i++) {
		CMIOObjectID device = ids[i];
		if (alreadyListening(device) || listenedCount >= MAX_LISTENED_DEVICES) {
			continue;
		}
		CMIOObjectAddPropertyListenerBlock(device, &runningAddress, listenerQueue(),
			^(UInt32 numberAddresses, const CMIOObjectPropertyAddress addresses[]) {
				goCameraChanged();
			});
		listenedDevices[listenedCount++] = device;
	}
	free(ids);
}

void wizcameraStartListeners(void) {
	dispatch_async(listenerQueue(), ^{
		CMIOObjectAddPropertyListenerBlock(kCMIOObjectSystemObject, &devicesAddress, listenerQueue(),
			^(UInt32 numberAddresses, const CMIOObjectPropertyAddress addresses[]) {
				registerDeviceListeners();
				goCameraChanged();
			});
		registerDeviceListeners();
	});
}
