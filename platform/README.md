# Platform

Platform is the server-side control plane, identity and certificate authority boundary, encrypted relay, persistence layer, operations backend, and management UI.

It must be based on the exact upstream source:

```text
repository:  mss-boot-io/mss-boot-admin
tag:         v1.3.7
tag object:  41c6517950f7f5f642418f5d4a49386e9c200b15
source SHA:  77b53d41092741eac62fa6418c0bdbf87413c7cd
Go:          1.26.6
```

The upstream source has not been imported merely because this directory exists. Check `platform/.upstream/` and `docs/memory/work-log.md` for actual import and verification status.

Platform must not store ABA/HC endpoint private keys or plaintext ACP payloads in the default Opaque Mode.
