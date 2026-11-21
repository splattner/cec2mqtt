# Changelog

## 1.0.0 (2026-11-21)


### Features

* **config:** validate the configuration on startup ([#20](https://github.com/splattner/cec2mqtt/issues/20)) ([c76c949](https://github.com/splattner/cec2mqtt/commit/c76c9496b52dee989ed16872b79db088d4be0a55))
* **home-assistant:** add a Cec2Mqtt bridge device with standby all and rescan buttons ([#22](https://github.com/splattner/cec2mqtt/issues/22)) ([1ebe790](https://github.com/splattner/cec2mqtt/commit/1ebe790d1e5c639445532e0a6a71194d7fb1acbd))
* **home-assistant:** add diagnostic entities for every device ([#21](https://github.com/splattner/cec2mqtt/issues/21)) ([cb30d67](https://github.com/splattner/cec2mqtt/commit/cb30d67cd175b730e6ad53a74cfa6e5f49b37351))
* shut down cleanly on SIGINT/SIGTERM ([#18](https://github.com/splattner/cec2mqtt/issues/18)) ([a4dc111](https://github.com/splattner/cec2mqtt/commit/a4dc1116e13fc660798d7cb6d619bd215077a6c3))


### Bug Fixes

* **cec:** don't tear down libcec when no adapter was opened ([#27](https://github.com/splattner/cec2mqtt/issues/27)) ([e13b6fb](https://github.com/splattner/cec2mqtt/commit/e13b6fb18db887031e77b8058e766dbb88e69a0e))
* **cec:** include gocec in the repository, make it thread safe and close libcec on shutdown ([#24](https://github.com/splattner/cec2mqtt/issues/24)) ([d5ee08b](https://github.com/splattner/cec2mqtt/commit/d5ee08b1349bc3120f9e3e665e7cf91b93f972a4))
* **power:** publish power states in order ([#16](https://github.com/splattner/cec2mqtt/issues/16)) ([ed723c1](https://github.com/splattner/cec2mqtt/commit/ed723c1c5cd7ccd69c48a956492d4753bfff0ccc))
