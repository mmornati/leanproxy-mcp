class LeanproxyMcp < Formula
  desc "LeanProxy-MCP - A lightweight token firewall for MCP servers"
  homepage "https://github.com/mmornati/leanproxy-mcp"
  license "MIT"

  version "0.7.2"

  on_macos do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.7.2/leanproxy-mcp_0.7.2_darwin_arm64.tar.gz"
      sha256 "0019dfc4b32d63c1392aa264aed2253c1e0c2fb09216f8e2cc269bbfb8bb49b5"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.7.2/leanproxy-mcp_0.7.2_darwin_amd64.tar.gz"
      sha256 "0019dfc4b32d63c1392aa264aed2253c1e0c2fb09216f8e2cc269bbfb8bb49b5"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.7.2/leanproxy-mcp_0.7.2_linux_arm64.tar.gz"
      sha256 "0019dfc4b32d63c1392aa264aed2253c1e0c2fb09216f8e2cc269bbfb8bb49b5"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.7.2/leanproxy-mcp_0.7.2_linux_amd64.tar.gz"
      sha256 "0019dfc4b32d63c1392aa264aed2253c1e0c2fb09216f8e2cc269bbfb8bb49b5"
    end
  end

  def install
    bin.install "leanproxy-mcp"
  end

  test do
    system "#{bin}/leanproxy-mcp", "version"
  end
end
