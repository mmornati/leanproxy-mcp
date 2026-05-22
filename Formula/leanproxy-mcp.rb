class LeanproxyMcp < Formula
  desc "LeanProxy-MCP - A lightweight token firewall for MCP servers"
  homepage "https://github.com/mmornati/leanproxy-mcp"
  license "MIT"

  version "0.7.0"

  on_macos do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.7.0/leanproxy-mcp_0.7.0_darwin_arm64.tar.gz"
      sha256 "91b2a5a8a0c1674e204034dd44cf545b37b6d51eab446958ad420ff7582cb8ea"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.7.0/leanproxy-mcp_0.7.0_darwin_amd64.tar.gz"
      sha256 "83f754b583492174bb28530eb685a4c86902d4c863a29d9b09e2e25ef7c5f089"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.7.0/leanproxy-mcp_0.7.0_linux_arm64.tar.gz"
      sha256 "21cbd1c1e89634a0851430635acaa2cced0079d19bf8888d48c23f0b241d7430"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.7.0/leanproxy-mcp_0.7.0_linux_amd64.tar.gz"
      sha256 "a8a2763ccc6e5a096ef3720e0265ae16d2d1e1becdcb5d74f0da644f112c7a8a"
    end
  end

  def install
    bin.install "leanproxy-mcp"
  end

  test do
    system "#{bin}/leanproxy-mcp", "version"
  end
end
