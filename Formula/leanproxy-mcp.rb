class LeanproxyMcp < Formula
  desc "LeanProxy-MCP - A lightweight token firewall for MCP servers"
  homepage "https://github.com/mmornati/leanproxy-mcp"
  license "MIT"

  version "0.11"

  on_macos do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.11/leanproxy-mcp_0.11_darwin_arm64.tar.gz"
      sha256 "71d788aed5643fc165791d99d83967c8f00ee496a25baee4c6230e7984255598"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.11/leanproxy-mcp_0.11_darwin_amd64.tar.gz"
      sha256 "2015bb1134ff8f58b22fc036e5ccdcbd04d8c516173aed3e73daf9131dce9fbe"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.11/leanproxy-mcp_0.11_linux_arm64.tar.gz"
      sha256 "8a83cb8c69ad791bcda1f6e17c43b021cdd0d1f972331716b9ee41918548cda3"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.11/leanproxy-mcp_0.11_linux_amd64.tar.gz"
      sha256 "b6e9f4711232f7c6d41df07dad511786a4ede1296b7b8738e2bcdcce170f1668"
    end
  end

  def install
    bin.install "leanproxy-mcp"
  end

  test do
    system "#{bin}/leanproxy-mcp", "version"
  end
end
