# 14 · Code interpreter

> English | [中文](14-code-interpreter.zh.md)

**Blocked by**: 13

## What
- The Guest Agent forwards to the Jupyter kernel in the Template
- Each Sandbox has a default Execution Context, and more can be created explicitly
- Outputs are text, errors and PNG images

## Acceptance
- [ ] After `x = 1`, running `x + 1` gives 2
- [ ] Variables don't leak between two Execution Contexts
- [ ] A matplotlib plot comes back as a PNG
- [ ] Calling it on a Template without ipykernel returns an explicit "unsupported"
