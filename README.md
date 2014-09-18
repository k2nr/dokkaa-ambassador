# Dokkaa Ambassador

This is a part of [Dokkaa project](https://github.com/k2nr/dokkaa).
dokkaa-ambassador is an implementation of [Ambassador Pattern](https://docs.docker.com/articles/ambassador_pattern_linking/).

# Installation

Mostly you don't use dokkaa-ambassador directly.
Use [dokkaacfg](https://github.com/k2nr/dokkaacfg) to launch all dokkaa environment.

For those who want to use dokkaa-ambassador solely, here is the usage.

```
$ docker run --name __ambassador -v /var/run/docker.sock:/var/run/docker.sock -e DOCKER_HOST=unix:///var/run/docker.sock --dns=<service DNS IP> k2nr/dokkaa-ambassador
```

# How It Works

# Contributing

# License

MIT
