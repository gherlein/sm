# Template for Formula/skills-mapper.rb in the gherlein/homebrew-tap repository.
# GoReleaser (.goreleaser.yaml) generates this file automatically on each tag;
# use this template only to publish by hand. Replace VERSION and SHA256 with the
# release tag (without the leading v) and the sha256 of its source tarball:
#   curl -sL https://github.com/gherlein/sm/archive/refs/tags/vVERSION.tar.gz | sha256sum
class SkillsMapper < Formula
  desc "Map one store of AI agent skills into every coding agent's skills folder"
  homepage "https://github.com/gherlein/sm"
  url "https://github.com/gherlein/sm/archive/refs/tags/vVERSION.tar.gz"
  sha256 "SHA256"
  head "https://github.com/gherlein/sm.git", branch: "main"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(output: bin/"sm"), "./cmd/sm"
    system "go", "build", *std_go_args(output: bin/"ms"), "./cmd/ms"
  end

  test do
    system bin/"sm", "-h"
    system bin/"ms", "-h"
  end
end
