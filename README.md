# Wakatime Collector

Collector for Leader board stats on wakatime

## Build
requires govvv to bind the version variables
otherwise it will bind as empty or null
```bash
go build -ldflags="$(govvv -flags)" .
```
### Linux
```bash
env GOOS=linux GOARCH=amd64 go build -ldflags="$(govvv -flags)" -o wakatime_amd .
```

