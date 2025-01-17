# cfctl - Command Line Interface for SpaceONE

**cfctl** is a powerful command-line interface tool designed to interact with SpaceONE services. It provides a seamless way to manage and control your SpaceONE resources through the command line.

# Features

- **Dynamic Service Discovery**: Automatically discovers and interacts with available SpaceONE services
- **Multi-Environment Support**: Manages multiple environments (user/app) with easy switching
- **Secure Authentication**: Supports both user and application token-based authentication
- **Rich Output Formats**: Supports various output formats including table, yaml, json, and csv
- **Alias**: Configurable resource aliases for faster command execution

# 01. Installation

## Using Homebrew (Recommended)

```bash
brew tap cloudforet-io/tap
brew install cfctl
```

## Manual Installation

### macOS

**For Intel Mac**

```bash
wget "https://github.com/cloudforet-io/cfctl/releases/latest/download/cfctl_Darwin_x86_64.tar.gz"
```

```
tar xvf cfctl_Darwin_x86_64.tar.gz
chmod +x cfctl
mv cfctl /usr/local/bin/
```

**For Apple Silicon Mac**

```bash
wget "https://github.com/cloudforet-io/cfctl/releases/latest/download/cfctl_Darwin_arm64.tar.gz"
```

```bash
tar xvf cfctl_Darwin_arm64.tar.gz
chmod +x cfctl
mv cfctl /usr/local/bin/
```


### Linux

**For x86_64**

```bash
wget "https://github.com/cloudforet-io/cfctl/releases/latest/download/cfctl_Linux_x86_64.tar.gz"
```

```bash
tar xvf cfctl_Linux_x86_64.tar.gz
chmod +x cfctl
mv cfctl /usr/local/bin/
```

**For ARM64**

```bash
wget "https://github.com/cloudforet-io/cfctl/releases/latest/download/cfctl_Linux_arm64.tar.gz"
```

```bash
tar xvf cfctl_Linux_arm64.tar.gz
chmod +x cfctl
mv cfctl /usr/local/bin/
```

### Docker

**Pull the latest image**

```bash
docker pull cloudforet/cfctl:latest
```

**Create an alias for easier use**

bash

```bash
echo 'alias cfctl="docker run --rm -it -v $HOME/.cfctl:/root/.cfctl cloudforet/cfctl:latest"' >> ~/.bashrc
source ~/.bashrc
```

zsh

```bash
echo 'alias cfctl="docker run --rm -it -v $HOME/.cfctl:/root/.cfctl cloudforet/cfctl:latest"' >> ~/.zshrc
source ~/.zshrc
```

fish

```bash
echo 'alias cfctl="docker run --rm -it -v $HOME/.cfctl:/root/.cfctl cloudforet/cfctl:latest"' >> ~/.config/fish/config.fish
source ~/.config/fish/config.fish
```

### Windows
1. Download the latest Windows release from our [releases page](https://github.com/cloudforet-io/cfctl/releases)
2. Extract the `cfctl_Windows_x86_64.zip` file
3. Add the extracted directory to your system's PATH environment variable
4. Open PowerShell or Command Prompt and verify the installation:

```powershell
cfctl
```

# 02. Quick Start

## 2.1. Initialize `cfctl` configuration

```bash
cfctl setting init
```
