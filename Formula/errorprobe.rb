class Errorprobe < Formula
  desc "Real-time error detection for Docker containers and Kubernetes pods"
  homepage "https://github.com/Veverke/ErrorProbe"
  version "1.0.0"
  license "MIT"

  on_macos do
    on_arm do
      # version, url, and sha256 are updated automatically by the
      # release workflow on every tag push.
      url "https://github.com/Veverke/ErrorProbe/releases/download/v1.0.0/errorprobe-darwin-arm64"
      sha256 "f002da215155f3664fc70141d3bd9e5de50a58fb54dca6ec20878240a0c51ac7"
    end
  end

  def install
    bin.install "errorprobe-darwin-arm64" => "errorprobe"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/errorprobe --version")
  end
end
