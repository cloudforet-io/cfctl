# cfctl - Command Line Interface for SpaceONE

cfctl is a powerful command-line interface tool designed to interact with SpaceONE services. It provides a seamless way to manage and control your SpaceONE resources through the command line.

## Features

- **Dynamic Service Discovery**: Automatically discovers and interacts with available SpaceONE services
- **Multi-Environment Support**: Manages multiple environments (user/app) with easy switching
- **Secure Authentication**: Supports both user and application token-based authentication
- **Rich Output Formats**: Supports various output formats including table, yaml, json, and csv
- **Short Names**: Configurable resource aliases for faster command execution

## Installation

### Using Homebrew (macOS)

```bash
brew tap cloudforet-io/tap
brew install cfctl
```

### Manual Installation

Download the latest binary from [releases page](https://github.com/cloudforet-io/cfctl/releases)

## Quick Start

1. Initialize cfctl configuration:

```bash
cfctl setting init
```
