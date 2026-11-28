class LeanproxyMcp < Formula
  desc "LeanProxy-MCP - A lightweight token firewall for MCP servers"
  homepage "https://github.com/mmornati/leanproxy-mcp"
  license "MIT"

  version "1.2"

  on_macos do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v1.2/leanproxy-mcp_1.2_darwin_arm64.tar.gz"
      sha256 "b871dfdd1b41513cd221ec7c3841393cd40f45cd5d78a0dbbe9f4b22563fb679"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v1.2/leanproxy-mcp_1.2_darwin_amd64.tar.gz"
      sha256 "3cd93d8f6001fdc8d493cf02bd899535169975d1cca7139bc339c0d1cfb53497"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v1.2/leanproxy-mcp_1.2_linux_arm64.tar.gz"
      sha256 "d57eb223ea3d764b233ae2f556cc56b48f86d6daa21ae31f2f7adfa1f7b70c96"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v1.2/leanproxy-mcp_1.2_linux_amd64.tar.gz"
      sha256 "ebb797631cf4b658af5edefb8824e5dda2dbda958de2ad2036ccb1f0374bc1f1"
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
