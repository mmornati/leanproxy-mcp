class LeanproxyMcp < Formula
  desc "LeanProxy-MCP - A lightweight token firewall for MCP servers"
  homepage "https://github.com/mmornati/leanproxy-mcp"
  license "MIT"

  version "0.9.0"

  on_macos do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.9.0/leanproxy-mcp_0.9.0_darwin_arm64.tar.gz"
      sha256 "93d828c391141b289145e3a0830e298ef316638384379938b85d04c5b74cd8a0"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.9.0/leanproxy-mcp_0.9.0_darwin_amd64.tar.gz"
      sha256 "180c198dc94b9d3f7039603b1ac894b83ea836e738b3caa83c38560a6c7fee17"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.9.0/leanproxy-mcp_0.9.0_linux_arm64.tar.gz"
      sha256 "e60629b25184520ec958184fa25da30555f82aa63eed8e85601900581f3a3e69"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.9.0/leanproxy-mcp_0.9.0_linux_amd64.tar.gz"
      sha256 "61ee65fe2222a3cc3aa97b0d86c474fbdd4cab6422e7765e5bd7210febcc27a2"
    end
  end

  def install
    bin.install "leanproxy-mcp"
  end

  test do
    system "#{bin}/leanproxy-mcp", "version"
  end
end
