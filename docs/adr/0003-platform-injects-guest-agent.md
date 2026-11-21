# The platform injects the Guest Agent, not the Template

> English | [中文](0003-platform-injects-guest-agent.zh.md)

When a Sandbox is created, the platform puts the Guest Agent into it: as a read-only mount on Docker, or copied in when the image is converted to a rootfs on Firecracker. Templates never need to install it, so any existing OCI image can be registered as a Template as-is, including the hundreds of official SWE-bench evaluation images. The cost is that the platform handles injection itself and can't assume anything about the image. The code interpreter needs a Jupyter kernel, so only the built-in `python` Template (with ipykernel preinstalled) supports it, and other Templates return an explicit error.

**Amendment (2026-11-21, after research).** To keep "official images work unmodified" on Firecracker too, the injected Guest Agent runs as PID 1 and then starts the image's own entrypoint, applying its ENV, USER and WORKDIR ([ADR 0008](0008-guest-agent-lifecycle.md)). E2B instead installs systemd into each image at Template build; Daytona injects its daemon as PID 1, which is the approach we follow.

## Considered Options

- Each Template installs the Guest Agent itself: a simpler platform, but every existing image would need rebuilding before use, and the compatibility tests would no longer be "run the official images directly".
