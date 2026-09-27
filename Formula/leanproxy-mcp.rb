class LeanproxyMcp < Formula
  desc "LeanProxy-MCP - A lightweight token firewall for MCP servers"
  homepage "https://github.com/mmornati/leanproxy-mcp"
  license "MIT"

  version "1.0"

  on_macos do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v1.0/leanproxy-mcp_1.0_darwin_arm64.tar.gz"
      sha256 "37de767dfc47521beec8f970ee80d6e98224f278d8ec04c95f5e1c8f3e597224"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v1.0/leanproxy-mcp_1.0_darwin_amd64.tar.gz"
      sha256 "4bd43632141a0c66d14bf5019d844ca19259292a678363bac5f80114acea0264"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v1.0/leanproxy-mcp_1.0_linux_arm64.tar.gz"
      sha256 "b1b8918a92339700331b080775773e889958e43f83f63b26e2438d6a242a17e7"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v1.0/leanproxy-mcp_1.0_linux_amd64.tar.gz"
      sha256 "ad65d768fc3595000cc872227c68c042aeaeb6d8f7e34fe493a9f6bc8a9da7be"
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
