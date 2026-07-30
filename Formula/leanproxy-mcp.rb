class LeanproxyMcp < Formula
  desc "LeanProxy-MCP - A lightweight token firewall for MCP servers"
  homepage "https://github.com/mmornati/leanproxy-mcp"
  license "MIT"

  version "0.8.0"

  on_macos do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.8.0/leanproxy-mcp_0.8.0_darwin_arm64.tar.gz"
      sha256 "29179c49174d4dd0d6b8d92f69b0bacf0951f77009cf31dd1a754acb5afe4c2d"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.8.0/leanproxy-mcp_0.8.0_darwin_amd64.tar.gz"
      sha256 "9010b7bbef2714139757df796f3ed9cc6a7e8abcf5461bd70e57e8c0dd657d54"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.8.0/leanproxy-mcp_0.8.0_linux_arm64.tar.gz"
      sha256 "cddfbe0ea2fb1b9bfcbbe43e2db9efe10f8a044887e87766caee0f1c36120fc5"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.8.0/leanproxy-mcp_0.8.0_linux_amd64.tar.gz"
      sha256 "4bce30c53adf5afea90f855d27b033e3edd0240b9aa5c3bd7bdc40b9faf9702c"
    end
  end

  def install
    bin.install "leanproxy-mcp"
  end

  test do
    system "#{bin}/leanproxy-mcp", "version"
  end
end
