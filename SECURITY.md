# Security policy

## Supported versions

Piccolo is currently pre-1.0. Security fixes are applied to the latest release and the `main` branch.

## Reporting a vulnerability

Please do not open a public issue for a suspected vulnerability. Use GitHub's **Security advisories → Report a vulnerability** feature for this repository. If private reporting is unavailable, contact the maintainer through the address listed on their GitHub profile and include only enough information to establish a secure follow-up channel.

Include the affected version or commit, impact, reproduction steps, and any proposed mitigation. You should receive an acknowledgement within seven days.

## Deployment assumptions

Piccolo does not currently provide authentication, authorization, or TLS. Deploy Piccolo and Pi only on trusted networks, restrict listeners with network policy or firewalls, and use a reverse proxy or service mesh for identity and encryption. pprof is disabled by default and should remain private when enabled.

Database DSNs contain credentials. Pass them through a secret manager or protected process environment, and never include them in logs, issue reports, or committed configuration.
