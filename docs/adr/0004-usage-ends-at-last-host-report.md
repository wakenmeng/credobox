# When a Host is lost, Usage ends at its last successful report

> English | [中文](0004-usage-ends-at-last-host-report.zh.md)

The Control Plane alone derives Usage from lifecycle events. Each event has a unique ID, so a duplicate report is counted once. If a Host is lost, its Sandboxes have no end event, so the Usage interval each one was in ends at that Host's last successful report, not when reconciliation notices it is gone. This may undercount by a few seconds, but a Tenant never pays for a platform failure.

## Considered Options

- End the interval when reconciliation notices the Sandbox is gone: simpler, but it bills the Tenant for the time between the Host being lost and the platform noticing.
