## PowerBot (Orange Pi)

Lightweight Go scraper + Telegram notifier for `https://poweron.loe.lviv.ua/`, deployed as a `systemd` timer on an Orange Pi.

### Requirements
- Go toolchain on the Orange Pi.
- Telegram bot token and target chat/channel ID.
- Network access to `https://poweron.loe.lviv.ua/`.

### Files in this directory
- `powerbot.go` – single-file app.
- `powerbot.service` – oneshot service wrapper.
- `powerbot.timer` – periodic trigger.

### Build (on the Orange Pi)
```sh
# If your Orange Pi is 64-bit:
GOOS=linux GOARCH=arm64 go build -o powerbot powerbot.go
# If 32-bit armhf, use GOARCH=arm instead.
```

### Install
```sh
sudo install -m 755 powerbot /usr/local/bin/powerbot
sudo install -m 644 powerbot.service /etc/systemd/system/powerbot.service
sudo install -m 644 powerbot.timer /etc/systemd/system/powerbot.timer
sudo mkdir -p /var/lib/powerbot
```

### Configure environment
Edit `/etc/systemd/system/powerbot.service` and set:
- `POWERBOT_TOKEN=...`
- `POWERBOT_CHAT_ID=...` (channel ID or chat ID)
- Optional: `POWERBOT_STATE=/var/lib/powerbot/state.json` (default shown)
- Optional test mode: `POWERBOT_TEST_FILE=/path/to/sample.html`

After edits:
```sh
sudo systemctl daemon-reload
```

### Run and enable timer
```sh
sudo systemctl enable --now powerbot.timer
# On-demand run:
sudo systemctl start powerbot.service
```

### Logs
```sh
journalctl -u powerbot.service -e
```

### Data retention
- State JSON kept at `POWERBOT_STATE` path; only last two days are stored.

### Update flow
- Timer runs (default: every 10 minutes) via `powerbot.timer`.
- Posts initial schedule for today/tomorrow; sends `upd. 😩` when outage minutes increase, `upd. 🍾` when they shrink or stay the same.
# PowerBot

Lightweight Go watcher that scrapes `https://poweron.loe.lviv.ua/` for outage schedules (groups 4.1 and 6.1) and posts updates to a Telegram channel. Intended to run on low-resource boards (e.g., Orange Pi) via systemd timer.

## Prerequisites
- Go toolchain on the target box (or cross-compile elsewhere).
- Telegram bot token with admin rights in the target channel.
- Network access to `https://poweron.loe.lviv.ua/`.

## Build
On the Orange Pi (ARM64 example):
```sh
GOOS=linux GOARCH=arm64 go build -o /usr/local/bin/powerbot powerbot.go
```
Adjust `GOARCH` if your board differs (e.g., `arm` for 32‑bit).

## Configuration
Environment variables (set in the systemd service):
- `POWERBOT_TOKEN` – Telegram bot token.
- `POWERBOT_CHAT_ID` – Channel/chat id (e.g., `-1001234567890`).
- `POWERBOT_STATE` – Path to state file (default `/var/lib/powerbot/state.json`).
- `POWERBOT_TEST_FILE` – Optional path to a local HTML/TXT file for offline/testing mode; when set, HTTP fetch is skipped.

Ensure the state directory exists and is writable:
```sh
sudo mkdir -p /var/lib/powerbot
sudo chown root:root /var/lib/powerbot
```

## systemd setup
Place the provided unit files:
- `powerbot.service` → `/etc/systemd/system/powerbot.service`
- `powerbot.timer`   → `/etc/systemd/system/powerbot.timer`

Edit `powerbot.service` to set your env values (TOKEN, CHAT_ID, optional STATE/TEST file paths).

Reload and enable:
```sh
sudo systemctl daemon-reload
sudo systemctl enable --now powerbot.timer
```

The timer runs the bot every 10 minutes (see `OnUnitActiveSec` in `powerbot.timer`). Logs go to `journalctl -u powerbot.service`.

### If you keep the unit files inside the repo (no copying)
- Ensure `ExecStart=` in `powerbot.service` points to the absolute path of the built binary in your cloned repo (e.g., `/home/youruser/loebot/powerbot`).
- Option A: copy remains simplest; Option B: link in place:
  ```sh
  sudo systemctl link /home/youruser/loebot/powerbot.service
  sudo systemctl link /home/youruser/loebot/powerbot.timer
  sudo systemctl daemon-reload
  sudo systemctl enable --now powerbot.timer
  ```
- If you rebuild the binary in-repo, no need to re-copy—`ExecStart` will use the same path.

## Manual runs
```sh
sudo systemctl start powerbot.service   # run once now
sudo systemctl status powerbot.service  # view last run
journalctl -u powerbot.service -n 100   # tail logs
```

## Testing with a local file
Set `POWERBOT_TEST_FILE=/path/to/sample.html` in the service (or export it before running the binary manually). Modify the sample file to simulate site changes; the bot will apply the same posting/update logic without hitting the network.

## What it posts
- New schedule for a date: `графік на DD.MM` (bold) + lines for 6.1 (power) and 4.1 (water).
- Updates: `upd. 😩` if outage minutes increased, otherwise `upd. 🍾`, then the same lines.
- Text mapping: “Електроенергія є.” → “не вимикатимуть”; otherwise keeps the “немає з HH:MM до HH:MM” text.

## Resource notes
- Single Go binary, stdlib only; uses a short-lived process triggered by systemd timer (lowest idle overhead).


