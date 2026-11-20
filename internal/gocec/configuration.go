package gocec

/*
#include <stdio.h>
#include <stdlib.h>
#include <libcec/cecc.h>

void setDeviceName(libcec_configuration *config, char *name)
{
	snprintf(config->strDeviceName, sizeof(config->strDeviceName), "%s", name);
}
*/
import "C"

import "unsafe"

type Configuration struct {
	configuration  C.libcec_configuration
	callbacks      *callbacks
	callbacksIndex int
}

func NewConfiguration(deviceName string, activateSource bool) (config *Configuration) {
	config = &Configuration{}
	C.libcec_clear_configuration(&config.configuration)
	config.configuration.clientVersion = C.LIBCEC_VERSION_CURRENT
	config.SetDeviceName(deviceName)
	config.configuration.deviceTypes.types[0] = C.CEC_DEVICE_TYPE_RECORDING_DEVICE
	config.SetActivateSource(activateSource)

	initialiseCallbacks(config)

	return
}

func (config *Configuration) SetDeviceName(name string) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	C.setDeviceName(&config.configuration, cName)
}

func (config *Configuration) SetActivateSource(activate bool) {
	config.configuration.bActivateSource = convertBool(activate)
}

func (config *Configuration) SetMonitorOnly(monitor bool) {
	config.configuration.bMonitorOnly = convertBool(monitor)
}

func convertBool(value bool) C.uchar {
	if value {
		return C.uchar(1)
	} else {
		return C.uchar(0)
	}
}
