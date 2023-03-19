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
    # workingDir: /Users/abdus/dev/ideas/payton
    command:  # Command to execute. Message text will be passed as commandline argument.
      - python3
      - -c
      - |-
        import sys
        import time
        print(sys.argv)
        text = sys.argv[2]
        time.sleep(3)
        print(f'received {text}')
```

## TODO

- Stream output and display progress