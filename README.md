# telecmd

A simple utility for running commands via Telegram messages.

## Usage

```shell
telecmd --token '123:token' config.yaml
```

## Configuration

Bot token can be passed with `--token` option or as an environment variable.

```env
TELEGRAM_BOT_TOKEN=13256:token
```

Rules are defined in `config.yaml`

```yaml
commandTimeout: 1s  # Anything parseable by time.ParseDuration
rules:
  - name: echo
    pattern: "/start"  # Regex to match incoming messages
    workingDir: /path/to/cwd
    # useStdin: true  # Pass message text in stdin 
    env:
      - PYTHONIOENCODING=utf-8
      - PYTHONLEGACYWINDOWSSTDIO=utf-8
      - PYTHONUTF8=1
    command:  # Command to execute. Message text will be passed as commandline argument.
      - python3
      - -c
      - |-
        import sys
        import os
        
        print(os.argv)
        for k in sorted(os.environ):
          print(f'{k}={os.environ[k]}')
```

## TODO

- Stream output and display progress