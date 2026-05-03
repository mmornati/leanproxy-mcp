class Leanproxy < Formula
  include Language::Python::Virtualenv

  desc "LeanProxy MCP - A JSON-RPC streaming proxy with token validation"
  homepage "https://github.com/leanproxy/leanproxy-mcp"
  url "https://github.com/leanproxy/leanproxy-mcp/releases/download/v#{version}/leanproxy-#{version}-darwin-amd64.tar.gz"
  version "#{version}"
  sha256 ""

  depends_on "go" => :build

  def install
    bin.install "leanproxy"
    completion_dir = etc/"bash_completion.d"
    completion_dir.mkpath
    completion_dir.install "completions/leanproxy.bash" if File.exist?("completions/leanproxy.bash")
    zsh_completion_dir = share/"zsh/site-functions"
    zsh_completion_dir.mkpath
    zsh_completion_dir.install "_leanproxy" if File.exist?("_leanproxy")
  end

  def caveats
    <<~EOS
      Installation complete! You can now use leanproxy.

      To enable shell completion, add to your shell configuration:

      For bash (~/.bashrc):
        source /etc/bash_completion.d/leanproxy

      For zsh (~/.zshrc):
        autoload -U compinit; compinit
        fpath=(#{HOMEBREW_PREFIX}/share/zsh/site-functions $fpath)

      Run 'leanproxy version' to verify installation.
    EOS
  end

  test do
    system "#{bin}/leanproxy", "version"
  end
end