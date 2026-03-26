class Tailhopper < Formula
  desc "Use multiple Tailscale tailnets at the same time"
  homepage "https://github.com/jcambass/tailhopper"
  url "https://github.com/jcambass/tailhopper/archive/refs/tags/vPLACEHOLDER_VERSION.tar.gz"
  sha256 "PLACEHOLDER_SHA256"
  license "MIT"
  head "https://github.com/jcambass/tailhopper.git", branch: "main"

  livecheck do
    url :stable
    strategy :github_latest
  end

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w -X main.version=#{version}"), "./cmd/tailhopper"
    (var/"tailhopper").mkpath
  end

  service do
    run [opt_bin/"tailhopper"]
    environment_variables HTTP_PORT: ENV.fetch("TAILHOPPER_HTTP_PORT", "8888")
    keep_alive true
    working_dir var/"tailhopper"
    log_path var/"log/tailhopper.log"
    error_log_path var/"log/tailhopper.log"
  end

  test do
    assert_equal "#{version}\n", shell_output("#{bin}/tailhopper --version")
  end

  def caveats
    <<~EOS
      Tailhopper stores its state file at:
        #{var}/tailhopper/tailhopper.json

      Logs are available at:
        #{var}/log/tailhopper.log

      Uninstall does not remove state/log files. To remove them:
        rm -rf "#{var}/tailhopper" "#{var}/log/tailhopper.log"

      To run on a custom dashboard port:
        TAILHOPPER_HTTP_PORT=9999 brew services restart tailhopper

      Dashboard: http://localhost:8888
    EOS
  end
end
