![Baton Logo](./baton-logo.png)

#

`baton-lucidchart` [![Go Reference](https://pkg.go.dev/badge/github.com/conductorone/baton-lucidchart.svg)](https://pkg.go.dev/github.com/conductorone/baton-lucidchart) ![main ci](https://github.com/conductorone/baton-lucidchart/actions/workflows/main.yaml/badge.svg)

`baton-lucidchart` is a connector for built using the [Baton SDK](https://github.com/conductorone/baton-sdk).

Check out [Baton](https://github.com/conductorone/baton) to learn more the project in general.

# Getting Started

1. Create an api key from [Lucidchart](https://developer.lucid.co/reference/creating-a-key)
    1. Required Api Permission
        1. FolderRead
        2. DocumentRead
2. Create oAuth2 client from [Lucidchart](https://developer.lucid.co/reference/client-creation)
    1. Generate the code using `authorizeAccount` https://developer.lucid.co/reference/obtaining-an-access-token
    2. Use the code or token/refresh-token on the connector

## Usage

```
baton-lucidchart \
    --lucid-client-id="" \
    --lucid-client-secret="" \
    --lucid-redirect-url="" \
    --lucid-api-key="" \
    --lucid-code=""
```

## brew

```
brew install conductorone/baton/baton conductorone/baton/baton-lucidchart
baton-lucidchart
baton resources
```

## docker

```
docker run --rm -v $(pwd):/out -e BATON_DOMAIN_URL=domain_url -e BATON_API_KEY=apiKey -e BATON_USERNAME=username ghcr.io/conductorone/baton-lucidchart:latest -f "/out/sync.c1z"
docker run --rm -v $(pwd):/out ghcr.io/conductorone/baton:latest -f "/out/sync.c1z" resources
```

## source

```
go install github.com/conductorone/baton/cmd/baton@main
go install github.com/conductorone/baton-lucidchart/cmd/baton-lucidchart@main

baton-lucidchart

baton resources
```

# Data Model

`baton-lucidchart` will pull down information about the following resources:

- Users

# Contributing, Support and Issues

We started Baton because we were tired of taking screenshots and manually
building spreadsheets. We welcome contributions, and ideas, no matter how
small&mdash;our goal is to make identity and permissions sprawl less painful for
everyone. If you have questions, problems, or ideas: Please open a GitHub Issue!

See [CONTRIBUTING.md](https://github.com/ConductorOne/baton/blob/main/CONTRIBUTING.md) for more details.

# `baton-lucidchart` Command Line Usage

```
baton-lucidchart

Usage:
  baton-lucidchart completion [command]

Available Commands:
  bash        Generate the autocompletion script for bash
  fish        Generate the autocompletion script for fish
  powershell  Generate the autocompletion script for powershell
  zsh         Generate the autocompletion script for zsh

Flags:
  -h, --help   help for completion

Global Flags:
      --client-id string       The client ID used to authenticate with ConductorOne ($BATON_CLIENT_ID)
      --client-secret string   The client secret used to authenticate with ConductorOne ($BATON_CLIENT_SECRET)
  -f, --file string            The path to the c1z file to sync with ($BATON_FILE) (default "sync.c1z")
      --log-format string      The output format for logs: json, console ($BATON_LOG_FORMAT) (default "json")
      --log-level string       The log level: debug, info, warn, error ($BATON_LOG_LEVEL) (default "info")
  -p, --provisioning           This must be set in order for provisioning actions to be enabled ($BATON_PROVISIONING)
      --skip-full-sync         This must be set to skip a full sync ($BATON_SKIP_FULL_SYNC)
      --ticketing              This must be set to enable ticketing support ($BATON_TICKETING)

Use "baton-lucidchart completion [command] --help" for more information about a command.
```
