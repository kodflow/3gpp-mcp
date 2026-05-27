{
  "mcpServers": {
    "github": {
      "command": "docker",
      "args": [
        "run",
        "-i",
        "--rm",
        "-e",
        "GITHUB_PERSONAL_ACCESS_TOKEN",
        "ghcr.io/github/github-mcp-server:latest"
      ],
      "env": {
        "GITHUB_PERSONAL_ACCESS_TOKEN": "{{GITHUB_TOKEN}}"
      }
    },
    "gitlab": {
      "command": "npx",
      "args": [
        "-y",
        "@zereight/mcp-gitlab@latest"
      ],
      "env": {
        "GITLAB_PERSONAL_ACCESS_TOKEN": "{{GITLAB_TOKEN}}",
        "GITLAB_API_URL": "{{GITLAB_API_URL:-https://gitlab.com/api/v4}}"
      }
    }
  }
}
