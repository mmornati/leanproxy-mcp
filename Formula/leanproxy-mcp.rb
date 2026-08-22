class LeanproxyMcp < Formula
  desc "LeanProxy-MCP - A lightweight token firewall for MCP servers"
  homepage "https://github.com/mmornati/leanproxy-mcp"
  license "MIT"

  version "0.9.1"

  on_macos do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.9.1/leanproxy-mcp_0.9.1_darwin_arm64.tar.gz"
      sha256 "3c724c53885753c23071ae71c70826ae1e505eee1ccfa4f1190fa8b5c8d1b6ce"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.9.1/leanproxy-mcp_0.9.1_darwin_amd64.tar.gz"
      sha256 "4ebfaf2da22183304371e24f8c1e62ff82ddfa0accf7773aa3bf6d0e4e3baa0c"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.9.1/leanproxy-mcp_0.9.1_linux_arm64.tar.gz"
      sha256 "ea3fc4c6eb3398f667a0db53caea91ae70fab07c744d867bbd13bf7be89b52c6"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.9.1/leanproxy-mcp_0.9.1_linux_amd64.tar.gz"
      sha256 "b356f71a501a6c5034800822029480eea452f2f8aecdce30e233a82f3c4fe389"
    end
  end

  def install
    bin.install "leanproxy-mcp"
  end

  test do
    system "#{bin}/leanproxy-mcp", "version"
  end
end
