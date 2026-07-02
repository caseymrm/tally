# WizCamera

A macOS menu bar app that turns on a smart light outside your office when
your camera is in use, so the household knows you're in a meeting.

- 🟢 green dot in the menu bar when you're available, 🔴 red when you're busy
- When any app (Zoom, Google Meet, FaceTime, ...) turns on your camera, your
  light turns red; when the camera turns off, the light turns off
- Click the dot to override the status — Force Busy or Force Available for
  5 minutes, 1 hour, or until you turn it off
- Pick which light this computer controls from the same menu (scans your
  network for bulbs)
- Optional Start at Login

Built with [menuet](https://github.com/caseymrm/menuet).

## How it works

Camera detection uses CoreMediaIO's `kCMIODevicePropertyDeviceIsRunningSomewhere`
property, which reports whether *any* process has a capture device open — no
log parsing, no per-app integration, and it doesn't require camera
permission. Change notifications come from CMIO property listeners, with a
low-frequency poll as a safety net.

Wiz bulbs are controlled over their local UDP protocol (JSON on port 38899,
no hub, no cloud, no auth). Bulbs are discovered with a broadcast probe and
identified by MAC address, so the app finds the bulb again if DHCP hands it
a new IP.

While you're in a meeting the app re-asserts the red state once a minute, so
the light recovers from power blips. When you're not in a meeting it leaves
the bulb alone (after turning it off once), so the bulb can still be used as
a normal light.

## Building

Requires macOS and Go.

```
make run
```

builds `WizCamera.app` and launches it. `go run .` also works for quick
iteration, but Start at Login wants a real bundle path, so prefer the app
bundle for daily use.

## Adding other brands of light

`pkg/light` defines the provider interface:

```go
type Provider interface {
    Name() string
    Discover(ctx context.Context) ([]light.Config, error)
    Connect(cfg light.Config) (light.Light, error)
}
```

`pkg/light/wiz` is the reference implementation. A new brand (LIFX is the
next obvious one — it has a similar local UDP protocol) just implements
`Provider`, calls `light.Register` in its `init`, and gets a blank import in
`main.go`; discovered lights from every provider show up together in the
menu. Light identity (`Config.ID`) should be something stable like a MAC
address, not an IP.

A future idea is to associate the selected light with the current Wi-Fi
network, so a laptop can control different lights at home and at the office.
`Config` is persisted as JSON with that kind of extension in mind.
