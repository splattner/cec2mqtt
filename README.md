# Cec2Mqtt
> [!NOTE]
> This project is a continuation of [RobertMe/cec2mqtt](https://github.com/RobertMe/cec2mqtt), originally created by
> [Robert Meijers](https://github.com/RobertMe). See [Credits](#credits).

cec2mqtt enables you to read the status and control your CEC enabled devices using MQTT.
Currently its supported features are:
* Reading the power status (on/off) of devices
* Powering on and off devices
* Reading which device is active, and switching to a device
* Reading and changing the volume and mute status of the audio system (AV receiver / soundbar)
* Sending remote control key presses to devices, and receiving key presses from the TV remote
* Home Assistant integration for auto discovery

# Requirements
cec2mqtt currently works on the following hardware:
* Generic x64 (Intel / AMD) computers, ARMv7 (32 bit), and ARMv8 (64) bit devices using the Pulse-Eight HDMI-CEC adapter
* Any device running Linux with kernel support for CEC (like a Raspberry Pi)
Testing has been done both on a generic x64 computer using the Pulse-Eight HDMI-CEC adapter and on a Raspberry Pi.

## Installation
The easiest way to run cec2mqtt is by using the Docker images, which work on all supported platforms (amd64, arm/v7 and arm64):

* ``ghcr.io/splattner/cec2mqtt:<version>`` (e.g. ``0.1.0``, ``0.1``) or ``latest`` for released versions, see the [releases](https://github.com/splattner/cec2mqtt/releases).
* ``ghcr.io/splattner/cec2mqtt:edge`` for the latest development version, built from every commit on ``master``.

Release images are signed with [cosign](https://github.com/sigstore/cosign) (keyless) and carry an SPDX SBOM attestation. The SBOM is also attached
to every GitHub release. To verify an image:
```console
cosign verify ghcr.io/splattner/cec2mqtt:<version> \
  --certificate-identity-regexp '^https://github.com/splattner/cec2mqtt/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
cosign verify-attestation --type spdxjson ghcr.io/splattner/cec2mqtt:<version> \
  --certificate-identity-regexp '^https://github.com/splattner/cec2mqtt/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Running cec2mqtt can be done using:
```console
docker run -v /path/to/data/directory:/data/cec2mqtt --device=/dev/cec0 ghcr.io/splattner/cec2mqtt:edge
```
``/dev/cec0`` can be replaced with another CEC device if the system exposes more. Or use ``/dev/ttyACM0`` or equivalent if your kernel doesn't expose
any CEC devices and you're using the Pulse-Eight HDMI-CEC adapter.

When using docker-compose the following ``docker-compose.yaml`` can be used as a starting point:
```yaml
services:
  cec2mqtt:
    container_name: cec2mqtt
    image: ghcr.io/splattner/cec2mqtt:edge
    volumes:
      - ./data:/data/cec2mqtt
    devices:
      - /dev/cec0
    restart: unless-stopped
```

## Configuration
Configuring cec2mqtt is done by a YAML file which must be created before the first start. The file must be
placed in the data directory and be called ``config.yaml``. Required options to configure are the MQTT host and base topic.
A minimal configuration file looks like this:
```yaml
mqtt:
  host: 1.2.3.4:1883
  base_topic: cec2mqtt
```

To enable the Home Assistant integration the following configuration must be added:
```yaml
home_assistant:
  enable: true
```
Enabling this integration is the recommended way to use cec2mqtt in combination with Home Assistant as it removes the requirement to manually
configure the entities in Home Assistant.

Optionally an MQTT state topic can be configured, on which cec2mqtt publishes the birth message while running and the will
message once stopped. This is recommended when using the Home Assistant integration, so the entities show as unavailable while
cec2mqtt isn't running. The messages default to ``online`` and ``offline``.
```yaml
mqtt:
  state_topic: cec2mqtt/state
  birth_message: online
  will_message: offline
```

A complete example of the configuration is the following:
```yaml
mqtt:
    host: 1.2.3.4:1883
    username: User
    password: P@ssw0rd
    state_topic: cec2mqtt/state
    birth_message: online
    will_message: offline
    base_topic: cec2mqtt
    client_id: cec2mqtt
cec:
    adapter: /dev/cec0
home_assistant:
    enable: true
    discovery_prefix: homeassistant
    birth_topic: homeassistant/status
    birth_payload: online
```

``host`` is the address of the MQTT broker, like ``1.2.3.4:1883``, ``tcp://broker:1883``, ``ssl://broker:8883``,
``wss://broker/mqtt`` or ``unix:///run/mosquitto.sock``. Without a scheme ``tcp://`` is used.

The configuration is checked on startup. cec2mqtt doesn't start when required options are missing, the broker address is
invalid, or a topic contains the MQTT wildcards ``+`` or ``#``, and logs all problems at once. Unknown options, which are
usually typos like ``base-topic``, are logged as warnings.

``client_id`` sets the MQTT client ID; a random one is used when it's not set. ``cec.adapter`` selects the CEC adapter to use when the
system has more than one (for example ``/dev/cec1`` or ``/dev/ttyACM0``); by default the first adapter found is used.

``birth_topic`` and ``birth_payload`` are used to detect Home Assistant restarts, after which all entities and states are republished.
They default to ``<discovery_prefix>/status`` and ``online``, matching Home Assistant's defaults.

## MQTT topics
All topics are prefixed with ``<base_topic>/<mqtt_topic of the device>/``, for example ``cec2mqtt/TV/power``.

| Topic | Direction | Payload |
| --- | --- | --- |
| ``power`` | state | ``on``, ``off`` or ``unknown`` |
| ``power/set`` | command | ``on`` or ``off`` |
| ``is_active_source`` | state | ``on`` or ``off`` |
| ``is_active_source/set`` | command | ``on``: wakes the TV and switches to this device |
| ``source`` | state | TV only: ``mqtt_topic`` of the active device, or ``None`` |
| ``source/set`` | command | TV only: ``mqtt_topic`` of the device to switch to |
| ``key/set`` | command | Name of a key to send, e.g. ``select``, ``up``, ``play``, ``pause``, ``root_menu``, ``number_1`` (see [keys.go](keys.go) for all names) |
| ``remote_key`` | event | ``{"event_type": "<key name>"}`` when the device sends a key press to cec2mqtt (e.g. the TV forwarding its remote while cec2mqtt is the active source) |
| ``volume`` | state | ``0`` - ``100`` (audio system only) |
| ``volume/set`` | command | ``up`` or ``down`` (audio system only) |
| ``mute`` | state | ``on`` or ``off`` (audio system only) |
| ``mute/set`` | command | ``on``, ``off`` or ``toggle`` (audio system only) |
| ``physical_address`` | state | Physical address, i.e. the HDMI port the device is connected to, e.g. ``1.0.0.0`` |
| ``logical_address`` | state | Logical address (device type) on the CEC bus, e.g. ``Playback device 1 (4)`` |
| ``vendor`` | state | Vendor reported by the device, e.g. ``Samsung`` |

Keys can be sent from Home Assistant using the ``mqtt.publish`` action:
```yaml
action: mqtt.publish
data:
  topic: cec2mqtt/Player/key/set
  payload: play
```

## Home Assistant
With the Home Assistant integration enabled, every CEC device shows up as a device in Home Assistant with these entities:

| Entity | Type | Devices |
| --- | --- | --- |
| Power | switch | all |
| Active source | binary sensor | all |
| Source | select, listing all devices | TV |
| Volume | sensor (%) | audio system |
| Volume up / Volume down | button | audio system |
| Mute | switch | audio system |
| Remote key | event | TV, and other devices once they send a key press |
| Physical address, Logical address, Vendor | diagnostic sensors | all |

cec2mqtt uses Home Assistant's device based MQTT discovery (one discovery message per device). Entities created by earlier
versions, which used one discovery message per entity, are migrated automatically and keep their entity ID, name and history.

### Media player
Home Assistant's MQTT integration doesn't support media players, but the entities above can be combined into one using the
[Universal Media Player](https://www.home-assistant.io/integrations/universal/). This gives you a regular media player card
with power, source selection and volume controls. Add this to ``configuration.yaml`` and adjust the entity IDs to match yours:
```yaml
media_player:
  - platform: universal
    name: Living room TV
    unique_id: living_room_tv
    children: []
    commands:
      turn_on:
        action: switch.turn_on
        target:
          entity_id: switch.tv_power
      turn_off:
        action: switch.turn_off
        target:
          entity_id: switch.tv_power
      select_source:
        action: select.select_option
        target:
          entity_id: select.tv_source
        data:
          option: "{{ source }}"
      # Remove the volume commands and is_volume_muted if there is no audio system
      volume_up:
        action: button.press
        target:
          entity_id: button.receiver_volume_up
      volume_down:
        action: button.press
        target:
          entity_id: button.receiver_volume_down
      volume_mute:
        action: switch.toggle
        target:
          entity_id: switch.receiver_mute
    attributes:
      state: switch.tv_power
      source: select.tv_source
      source_list: select.tv_source|options
      is_volume_muted: switch.receiver_mute
```

### Command line options
| Option | Default | Description |
| --- | --- | --- |
| ``-data-dir`` | ``/data/cec2mqtt/`` | Directory containing ``config.yaml`` and ``devices.yaml`` |
| ``-log-level`` | ``info`` | One of ``error``, ``warning``, ``info``, ``debug``, ``trace`` |
| ``-log-format`` | ``text`` | ``text`` or ``json`` |
| ``-log-cec-messages`` | ``false`` | Also log the internal libcec log (at debug level) |

When using Docker the options can be appended to the ``docker run`` command, or be set using ``command:`` in ``docker-compose.yaml``.

### Device configuration
Devices which have been found in the CEC network can be configured as well. For this you **must** first stop cec2mqtt. When Cec2Mqtt is stopped you
can open the devices.yaml file in the data directory. Here you can change the ``mqtt_topic`` which is used in MQTT.

Optionally ``ignore`` can be set to ``true`` to completely ignore a device after which no cec2mqtt doesn't support this device anymore and
you can't read the state nor control the device.

Note that under normal operations you must never change any of the other values like, ``id``, ``physical_address``, ``vendor_id`` and ``osd`` 
as these are used by cec2mqtt to remember and look up the device.

## Credits
cec2mqtt was originally created by [Robert Meijers](https://github.com/RobertMe) at
[RobertMe/cec2mqtt](https://github.com/RobertMe/cec2mqtt). He wrote the foundation of this project between 2019 and 2021:
the CEC handling for both libcec and the Linux kernel CEC API, the MQTT bridge, device discovery and the Home Assistant
integration. This repository started as a fork of his work and continues it with additional features and maintenance.
The full history of the original project, including all of Robert's commits, is preserved in this repository.

Many thanks to Robert for creating cec2mqtt and making it available!
