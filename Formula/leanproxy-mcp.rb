class LeanproxyMcp < Formula
  desc "LeanProxy-MCP - A lightweight token firewall for MCP servers"
  homepage "https://github.com/mmornati/leanproxy-mcp"
  license "MIT"

  version "0.6.0"

  on_macos do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.6.0/leanproxy-mcp_0.6.0_darwin_arm64.tar.gz"
      sha256 "a6b3f42665443c0fc0a4316048c057c7a5360bcd6f6f7a033ded2a77dc36c5a1"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.6.0/leanproxy-mcp_0.6.0_darwin_amd64.tar.gz"
      sha256 "5a7873b1105e2144f9302d15c2f1cc99ea4ee5d819df7e23d6dc61541e59ddc2"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.6.0/leanproxy-mcp_0.6.0_linux_arm64.tar.gz"
      sha256 "0eb03a81322b789f8f513809ffe32d68482d4fe1be53aef3a3d37c438ddb7bfb"
    end
    on_intel do
      url "https://github.com/mmornati/leanproxy-mcp/releases/download/v0.6.0/leanproxy-mcp_0.6.0_linux_amd64.tar.gz"
      sha256 "c91b1b631772b6936d4fa24079b473c8fd2bbd11a9490141952cb92759dfc9a6"
    end
  end

  def install
    bin.install "leanproxy-mcp"
  end

  test do
    system "#{bin}/leanproxy-mcp", "version"
  end
end
