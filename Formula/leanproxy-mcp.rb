class LeanproxyMcp < Formula
  desc "LeanProxy-MCP - A lightweight token firewall for MCP servers"
  homepage "https://github.com/mmornati/leanproxy-mcp"
  license "MIT"

  version "1.1.2"

  on_macos do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v1.1.2/leanproxy-mcp_1.1.2_darwin_arm64.tar.gz"
      sha256 "31c7e79af198cbb8fcc6597e19cb077fcbb3de7219ce5e3a85492f99ee129fbd"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v1.1.2/leanproxy-mcp_1.1.2_darwin_amd64.tar.gz"
      sha256 "18f1666b309ab92ce6cc0752a5b4bb58427ed337d0eb298f7fa321ce3ee3e33f"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v1.1.2/leanproxy-mcp_1.1.2_linux_arm64.tar.gz"
      sha256 "e5535e49c7057feb1d2aeb015783420190311663a2d1eace2e7af139ba3028f0"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v1.1.2/leanproxy-mcp_1.1.2_linux_amd64.tar.gz"
      sha256 "b1096613cfad989edcea0288c26a5a976bc831da6eb95b7bd674fdc7fa22232f"
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
