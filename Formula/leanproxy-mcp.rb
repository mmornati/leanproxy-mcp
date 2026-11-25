class LeanproxyMcp < Formula
  desc "LeanProxy-MCP - A lightweight token firewall for MCP servers"
  homepage "https://github.com/mmornati/leanproxy-mcp"
  license "MIT"

  version "1.1.1"

  on_macos do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v1.1.1/leanproxy-mcp_1.1.1_darwin_arm64.tar.gz"
      sha256 "4abed74aeac83f3beced4902265dea192917bd70781c514a8dbc176f17a7afeb"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v1.1.1/leanproxy-mcp_1.1.1_darwin_amd64.tar.gz"
      sha256 "8845aafcaca672bf8d92cf48e422d8d31603866fc3b72196ce194d667ba8d2af"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v1.1.1/leanproxy-mcp_1.1.1_linux_arm64.tar.gz"
      sha256 "bd74a90ad897776197e5094e842033afe165543b5a61d6acfa60b0d88d5e5cb8"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v1.1.1/leanproxy-mcp_1.1.1_linux_amd64.tar.gz"
      sha256 "05e4ae3031ca3ac4f365e09b9f57c0f16f311c4524b2892912f29bf4a56feead"
    end
  end

  def install
    bin.install "leanproxy-mcp"
    generate_completions_from_executable(bin/"leanproxy-mcp", "completion")
  end

  test do
    system "#{bin}/leanproxy-mcp", "version"
  end
end
