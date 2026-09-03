# ChatOpenApi frontend

This frontend is based on [hrmncode/ChatOpenApi](https://github.com/hrmncode/ChatOpenApi)
at commit `77602344204bedb26b535b26e621a3d156841d13` and is distributed under the
MIT license in `LICENSE`.

Local changes set `/v1` + `auto` as the default gateway, report the model chosen
by the router, and emit the production build into `gateway/web` for Go embedding.
