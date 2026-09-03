# Deployment

Deployment assets will cover:

- local development;
- Docker Compose;
- Platform on Kubernetes;
- ABA as a user service, systemd service, container, or Kubernetes sidecar/pod.

ABA remains outbound-only by default and does not require an ingress Service. Production deployment files must not embed credentials, private keys, recovery codes, or real user data.
