## [1.5.2](https://github.com/MicroTodoSuite/microservice-app-auth-api/compare/v1.5.1...v1.5.2) (2026-09-21)


### Bug Fixes

* **ci:** pin the promotion workflow past the conventions repair ([#33](https://github.com/MicroTodoSuite/microservice-app-auth-api/issues/33)) ([e483f05](https://github.com/MicroTodoSuite/microservice-app-auth-api/commit/e483f0511f3f25fb62b54b9a78081d0774c8aaba)), closes [#191](https://github.com/MicroTodoSuite/microservice-app-auth-api/issues/191) [#192](https://github.com/MicroTodoSuite/microservice-app-auth-api/issues/192) [#193](https://github.com/MicroTodoSuite/microservice-app-auth-api/issues/193) [#195](https://github.com/MicroTodoSuite/microservice-app-auth-api/issues/195) [#196](https://github.com/MicroTodoSuite/microservice-app-auth-api/issues/196) [#198](https://github.com/MicroTodoSuite/microservice-app-auth-api/issues/198) [MicroTodoSuite/microservice-app-gitops#205](https://github.com/MicroTodoSuite/microservice-app-gitops/issues/205)

## [1.5.1](https://github.com/MicroTodoSuite/microservice-app-auth-api/compare/v1.5.0...v1.5.1) (2026-09-14)


### Bug Fixes

* **ci:** repoint to the latest .github reusable workflow refs ([#30](https://github.com/MicroTodoSuite/microservice-app-auth-api/issues/30)) ([52f4fdf](https://github.com/MicroTodoSuite/microservice-app-auth-api/commit/52f4fdff5ed4601f09d8b8ae8f14be98f1c138f0)), closes [#142](https://github.com/MicroTodoSuite/microservice-app-auth-api/issues/142) [#19](https://github.com/MicroTodoSuite/microservice-app-auth-api/issues/19)

# [1.5.0](https://github.com/MicroTodoSuite/microservice-app-auth-api/compare/v1.4.0...v1.5.0) (2026-09-13)


### Features

* **metrics:** count accepted and rejected sign-ins in auth-api ([2347344](https://github.com/MicroTodoSuite/microservice-app-auth-api/commit/23473444a6a45730e46aff65edcfd6d8ba42454f))
* **metrics:** record auth-api metrics through opentelemetry ([4ce77cf](https://github.com/MicroTodoSuite/microservice-app-auth-api/commit/4ce77cf0de1872b78052a39b0083cccce164df17))
* **metrics:** record auth-api metrics through opentelemetry ([#28](https://github.com/MicroTodoSuite/microservice-app-auth-api/issues/28)) ([e9c14b6](https://github.com/MicroTodoSuite/microservice-app-auth-api/commit/e9c14b6bdc182838f54dcc22d9a8b1836aeb405b)), closes [MicroTodoSuite/microservice-app-gitops#136](https://github.com/MicroTodoSuite/microservice-app-gitops/issues/136) [MicroTodoSuite/microservice-app-gitops#136](https://github.com/MicroTodoSuite/microservice-app-gitops/issues/136)

# [1.4.0](https://github.com/MicroTodoSuite/microservice-app-auth-api/compare/v1.3.0...v1.4.0) (2026-09-13)


### Features

* **tracing:** trace auth-api sign-ins without tracing probes or losing the resilient client ([#26](https://github.com/MicroTodoSuite/microservice-app-auth-api/issues/26)) ([ec7ab2a](https://github.com/MicroTodoSuite/microservice-app-auth-api/commit/ec7ab2aa0df6b63441cdc723db6e272b42a1ea75)), closes [#123](https://github.com/MicroTodoSuite/microservice-app-auth-api/issues/123)

# [1.3.0](https://github.com/MicroTodoSuite/microservice-app-auth-api/compare/v1.2.1...v1.3.0) (2026-09-09)


### Bug Fixes

* **ci:** target replacement AWS account ([510ad0c](https://github.com/MicroTodoSuite/microservice-app-auth-api/commit/510ad0cc61b7ec505829fbf9b3eb22e60002f0c7))
* **security:** update vulnerable Go dependencies ([d801538](https://github.com/MicroTodoSuite/microservice-app-auth-api/commit/d8015380ada8cf77facbd093e0739e6bf13c2064))


### Features

* **us3:** auth-api health, correlation, telemetry, and resilience contract ([#16](https://github.com/MicroTodoSuite/microservice-app-auth-api/issues/16)) ([ed0347d](https://github.com/MicroTodoSuite/microservice-app-auth-api/commit/ed0347d441a881e5aeb7f4f38a497d4baa70787c))

## [1.2.1](https://github.com/MicroTodoSuite/microservice-app-auth-api/compare/v1.2.0...v1.2.1) (2026-08-24)


### Bug Fixes

* remove invalid trailing comma in login json example ([#15](https://github.com/MicroTodoSuite/microservice-app-auth-api/issues/15)) ([1f4f7ad](https://github.com/MicroTodoSuite/microservice-app-auth-api/commit/1f4f7ad23596d55718be0b3262a263f19e31b06d))

# [1.2.0](https://github.com/MicroTodoSuite/microservice-app-auth-api/compare/v1.1.1...v1.2.0) (2026-08-24)


### Bug Fixes

* **ci:** publish images to the migrated AWS account ([5973e4f](https://github.com/MicroTodoSuite/microservice-app-auth-api/commit/5973e4f880b4ce2b0199bdc05fac898dc40ac432))


### Features

* replace Zipkin with OpenTelemetry tracing and add latency histogram ([6853820](https://github.com/MicroTodoSuite/microservice-app-auth-api/commit/6853820e8407fb334c9c2bdcaa71c4a2176da8f6))

## [1.1.1](https://github.com/MicroTodoSuite/microservice-app-auth-api/compare/v1.1.0...v1.1.1) (2026-08-19)


### Bug Fixes

* use numeric runtime identity ([a800b2d](https://github.com/MicroTodoSuite/microservice-app-auth-api/commit/a800b2dde337aadea3bf5bca8bb12a055dbcfc9c))

# [1.1.0](https://github.com/MicroTodoSuite/microservice-app-auth-api/compare/v1.0.0...v1.1.0) (2025-04-25)


### Features

* **pipeline:** add update of pipeline ([96eda3d](https://github.com/MicroTodoSuite/microservice-app-auth-api/commit/96eda3dfbd89249960e8326ddd52f4be0bee09e0))

# 1.0.0 (2025-04-25)


### Bug Fixes

* **pipeline:** update pipeline ([073ee80](https://github.com/MicroTodoSuite/microservice-app-auth-api/commit/073ee8027ac13afbf806b58c30725dd118737446))


### Features

*  pipeline added ([86c1d46](https://github.com/MicroTodoSuite/microservice-app-auth-api/commit/86c1d46ad06e04b8959fafd06973399e8c526d0d))
* add microservice for auth ([2e58884](https://github.com/MicroTodoSuite/microservice-app-auth-api/commit/2e5888421fb327a12626d0b6ae7320e36dba8cba))
* **pipeline:** add pipeline of development ([5eaf8f7](https://github.com/MicroTodoSuite/microservice-app-auth-api/commit/5eaf8f7b4448ac8b0cf09bba54df0baf09fa12d4))
