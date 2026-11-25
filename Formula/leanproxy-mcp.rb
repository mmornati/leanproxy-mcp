class LeanproxyMcp < Formula
  desc "LeanProxy-MCP - A lightweight token firewall for MCP servers"
  homepage "https://github.com/mmornati/leanproxy-mcp"
  license "MIT"

  version "1.1"

  on_macos do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v1.1/leanproxy-mcp_1.1_darwin_arm64.tar.gz"
      sha256 "c0c8c8b2b8079a8939066b9a1391bf4ba8b85bb1b62eb2bee41ca624e65f44a6"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v1.1/leanproxy-mcp_1.1_darwin_amd64.tar.gz"
      sha256 "dda588fdc89265ac9fab4930d3e7d6f4a00e576050d6c54f5bce7396b8398d15"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v1.1/leanproxy-mcp_1.1_linux_arm64.tar.gz"
      sha256 "301c863f7a7bdfee74d92ce68dd7d137209248470e4722c2f777c1b801f9ed56"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v1.1/leanproxy-mcp_1.1_linux_amd64.tar.gz"
      sha256 "8b8697c0f92ee35a5b10ba008c1806d3da8487205ce6e949aed99eced6e8099f"
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
