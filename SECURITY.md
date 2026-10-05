# Security

## Reporting a vulnerability

Report it privately, through GitHub: **Security > Report a vulnerability** on
this repository. Please do not open a public issue for it.

Say what an attacker can do and how; a proof of concept against a local
instance helps. You will get an answer within a week.

Worth reporting, in particular: a way to get the provider's credentials or
address from the proxy, to watch or use the API without the proxy's
password, or to make the proxy fetch an address it did not give out.

## Supported versions

The latest release. Fixes are not backported to older versions.

## Keep in mind

The HDHomeRun tuner has no login by design: keep its port on your local
network. The proxy's own password travels in clear over HTTP: put the proxy
behind HTTPS (see the README) when it is reachable from the Internet.
