package integration_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
	"github.com/sclevine/spec"

	. "github.com/onsi/gomega"
	. "github.com/paketo-buildpacks/packit/v2/matchers"
)

func testUpdateBuildpack(t *testing.T, context spec.G, it spec.S) {
	var (
		withT      = NewWithT(t)
		Expect     = withT.Expect
		Eventually = withT.Eventually

		server       *httptest.Server
		buildpackDir string
	)

	context("when updating buildpacks it uses the CNB registry by default", func() {
		it.Before(func() {
			goRef, err := name.ParseReference("index.docker.io/paketobuildpacks/go-dist")
			Expect(err).ToNot(HaveOccurred())
			goImg, err := remote.Image(goRef)
			Expect(err).ToNot(HaveOccurred())

			rubyRef, err := name.ParseReference("paketobuildpacks/mri")
			Expect(err).ToNot(HaveOccurred())
			rubyImg, err := remote.Image(rubyRef)
			Expect(err).ToNot(HaveOccurred())

			goManifestPath := "/v2/paketo-buildpacks/go-dist/manifests/0.20.1"
			goBadVersionManifestPath := "/v2/paketo-buildpacks/go-dist/manifests/bad-version"
			goConfigPath := fmt.Sprintf("/v2/paketo-buildpacks/go-dist/blobs/%s", mustConfigName(t, goImg))
			goManifestReqCount := 0

			rubyManifestPath := "/v2/paketobuildpacks/mri/manifests/0.2.0"
			rubyConfigPath := fmt.Sprintf("/v2/paketobuildpacks/mri/blobs/%s", mustConfigName(t, rubyImg))
			rubyManifestReqCount := 0

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch req.URL.Path {
				case "/v1/":
					w.WriteHeader(http.StatusOK)

				case "/v2/":
					w.WriteHeader(http.StatusOK)

				case "/v1/buildpacks/paketo-buildpacks/go-dist":
					w.WriteHeader(http.StatusOK)
					_, err := fmt.Fprintln(w, `{
						"latest": {
							"version": "0.21.0"
						},
					  "versions": [
					    {
					      "version": "0.20.1"
					    },
					    {
					      "version": "0.20.2"
					    },
					    {
					      "version": "0.20.12"
					    },
					    {
					      "version": "0.21.0"
					    }
						]
					}`)
					Expect(err).NotTo(HaveOccurred())

				case "/v1/buildpacks/paketo-buildpacks/mri":
					w.WriteHeader(http.StatusOK)
					_, err := fmt.Fprintln(w, `{
						"latest": {
							"version": "0.2.0"
						},
					  "versions": [
					    {
					      "version": "0.2.0"
					    }
						]
					}`)
					Expect(err).NotTo(HaveOccurred())

				case "/v1/buildpacks/paketo-buildpacks/node-engine":
					w.WriteHeader(http.StatusOK)
					_, err := fmt.Fprintln(w, `{
						"latest": {
							"version": "0.20.22"
						},
					  "versions": [
					    {
					      "version": "0.20.22"
					    }
						]
					}`)
					Expect(err).NotTo(HaveOccurred())

				case goConfigPath:
					if req.Method != http.MethodGet {
						t.Errorf("Method; got %v, want %v", req.Method, http.MethodGet)
					}
					_, _ = w.Write(mustRawConfigFile(t, goImg))

				case goManifestPath:
					goManifestReqCount++
					if req.Method != http.MethodGet {
						t.Errorf("Method; got %v, want %v", req.Method, http.MethodGet)
					}
					_, _ = w.Write(mustRawManifest(t, goImg))

				case goBadVersionManifestPath:
					if req.Method != http.MethodGet {
						t.Errorf("Method; got %v, want %v", req.Method, http.MethodGet)
					}
					_, _ = w.Write(mustRawManifest(t, goImg))

				case rubyConfigPath:
					if req.Method != http.MethodGet {
						t.Errorf("Method; got %v, want %v", req.Method, http.MethodGet)
					}
					_, _ = w.Write(mustRawConfigFile(t, rubyImg))

				case rubyManifestPath:
					rubyManifestReqCount++
					if req.Method != http.MethodGet {
						t.Errorf("Method; got %v, want %v", req.Method, http.MethodGet)
					}
					_, _ = w.Write(mustRawManifest(t, rubyImg))

				case "/v2/some-repository/nonexistent-labels-id/tags/list":
					w.WriteHeader(http.StatusOK)
					_, err := fmt.Fprintln(w, `{
						  "tags": [
								"0.1.0",
								"0.2.0",
								"latest"
							]
					}`)
					Expect(err).NotTo(HaveOccurred())

				case "/v2/some-repository/nonexistent-labels-id/manifests/0.2.0":
					w.WriteHeader(http.StatusBadRequest)

				default:
					t.Fatalf("unknown path: %s", req.URL.Path)
				}
			}))

			buildpackDir, err = os.MkdirTemp("", "")
			Expect(err).NotTo(HaveOccurred())

			err = os.WriteFile(filepath.Join(buildpackDir, "buildpack.toml"), []byte(`
				api = "0.2"

				[buildpack]
					id = "some-composite-buildpack"
					name = "Some Composite Buildpack"
					version = "some-composite-buildpack-version"

				[metadata]
					include-files = ["buildpack.toml"]

				[[order]]
					[[order.group]]
						id = "paketo-buildpacks/go-dist"
						version = "0.20.1"

					[[order.group]]
						id = "paketo-buildpacks/mri"
						version = "0.2.0"

				[[order]]
					[[order.group]]
						id = "paketo-buildpacks/node-engine"
						version = "0.1.0"
						optional = true

					[[order.group]]
						id = "paketo-buildpacks/go-dist"
						version = "0.20.1"
			`), 0600)
			Expect(err).NotTo(HaveOccurred())

			err = os.WriteFile(filepath.Join(buildpackDir, "package.toml"), bytes.ReplaceAll([]byte(`
				[buildpack]
				uri = "build/buildpack.tgz"

				[[dependencies]]
				uri = "docker://REGISTRY-URI/paketobuildpacks/mri:0.2.0"

				[[dependencies]]
				uri = "docker://REGISTRY-URI/paketo-buildpacks/go-dist:0.20.1"

				[[dependencies]]
				uri = "urn:cnb:registry:paketo-buildpacks/node-engine@0.1.0"
			`), []byte(`REGISTRY-URI`), []byte(strings.TrimPrefix(server.URL, "http://"))), 0600)
			Expect(err).NotTo(HaveOccurred())
		})

		it.After(func() {
			server.Close()
			Expect(os.RemoveAll(buildpackDir)).To(Succeed())
		})

		it("updates the buildpack.toml and package.toml files", func() {
			command := exec.Command(
				path,
				"update-buildpack",
				"--buildpack-file", filepath.Join(buildpackDir, "buildpack.toml"),
				"--package-file", filepath.Join(buildpackDir, "package.toml"),
				"--api", server.URL,
			)

			buffer := gbytes.NewBuffer()
			session, err := gexec.Start(command, buffer, buffer)
			Expect(err).NotTo(HaveOccurred())

			Eventually(session).Should(gexec.Exit(0), func() string { return string(buffer.Contents()) })
			Expect(string(buffer.Contents())).To(ContainSubstring("Highest semver bump: minor"))

			buildpackContents, err := os.ReadFile(filepath.Join(buildpackDir, "buildpack.toml"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(buildpackContents)).To(MatchTOML(`
				api = "0.2"

				[buildpack]
					id = "some-composite-buildpack"
					name = "Some Composite Buildpack"
					version = "some-composite-buildpack-version"

				[metadata]
					include-files = ["buildpack.toml"]

				[[order]]
					[[order.group]]
						id = "paketo-buildpacks/go-dist"
						version = "0.21.0"

					[[order.group]]
						id = "paketo-buildpacks/mri"
						version = "0.2.0"

				[[order]]
					[[order.group]]
						id = "paketo-buildpacks/node-engine"
						version = "0.20.22"
						optional = true

					[[order.group]]
						id = "paketo-buildpacks/go-dist"
						version = "0.21.0"
			`))

			packageContents, err := os.ReadFile(filepath.Join(buildpackDir, "package.toml"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(packageContents)).To(MatchTOML(`
				[buildpack]
				uri = "build/buildpack.tgz"

				[[dependencies]]
				uri = "urn:cnb:registry:paketo-buildpacks/mri@0.2.0"

				[[dependencies]]
				uri = "urn:cnb:registry:paketo-buildpacks/go-dist@0.21.0"

				[[dependencies]]
				uri = "urn:cnb:registry:paketo-buildpacks/node-engine@0.20.22"
			`))
		})

		context("the --patch-only flag is set", func() {
			it("updates ONLY patch-level changes in the buildpack.toml and package.toml files", func() {
				command := exec.Command(
					path,
					"update-buildpack",
					"--buildpack-file", filepath.Join(buildpackDir, "buildpack.toml"),
					"--package-file", filepath.Join(buildpackDir, "package.toml"),
					"--api", server.URL,
					"--patch-only",
				)

				buffer := gbytes.NewBuffer()
				session, err := gexec.Start(command, buffer, buffer)
				Expect(err).NotTo(HaveOccurred())

				Eventually(session).Should(gexec.Exit(0), func() string { return string(buffer.Contents()) })
				Expect(session).Should(gbytes.Say("Highest semver bump: patch"))

				buildpackContents, err := os.ReadFile(filepath.Join(buildpackDir, "buildpack.toml"))
				Expect(err).NotTo(HaveOccurred())
				Expect(string(buildpackContents)).To(MatchTOML(`
				api = "0.2"

				[buildpack]
					id = "some-composite-buildpack"
					name = "Some Composite Buildpack"
					version = "some-composite-buildpack-version"

				[metadata]
					include-files = ["buildpack.toml"]

				[[order]]
					[[order.group]]
						id = "paketo-buildpacks/go-dist"
						version = "0.20.12"

					[[order.group]]
						id = "paketo-buildpacks/mri"
						version = "0.2.0"

				[[order]]
					[[order.group]]
						id = "paketo-buildpacks/node-engine"
						version = "0.1.0"
						optional = true

					[[order.group]]
						id = "paketo-buildpacks/go-dist"
						version = "0.20.12"
			`))

				packageContents, err := os.ReadFile(filepath.Join(buildpackDir, "package.toml"))
				Expect(err).NotTo(HaveOccurred())
				Expect(string(packageContents)).To(MatchTOML(`
				[buildpack]
				uri = "build/buildpack.tgz"

				[[dependencies]]
				uri = "urn:cnb:registry:paketo-buildpacks/mri@0.2.0"

				[[dependencies]]
				uri = "urn:cnb:registry:paketo-buildpacks/go-dist@0.20.12"

				[[dependencies]]
				uri = "urn:cnb:registry:paketo-buildpacks/node-engine@0.1.0"
			`))
			})

		})

		context("failure cases", func() {
			context("when the --buildpack-file flag is missing", func() {
				it("prints an error and exits non-zero", func() {
					command := exec.Command(
						path,
						"update-buildpack",
						"--package-file", filepath.Join(buildpackDir, "package.toml"),
					)

					buffer := gbytes.NewBuffer()
					session, err := gexec.Start(command, buffer, buffer)
					Expect(err).NotTo(HaveOccurred())

					Eventually(session).Should(gexec.Exit(1), func() string { return string(buffer.Contents()) })
					Expect(string(buffer.Contents())).To(ContainSubstring("Error: required flag(s) \"buildpack-file\" not set"))
				})
			})

			context("when the --package-file flag is missing", func() {
				it("prints an error and exits non-zero", func() {
					command := exec.Command(
						path,
						"update-buildpack",
						"--buildpack-file", filepath.Join(buildpackDir, "buildpack.toml"),
					)

					buffer := gbytes.NewBuffer()
					session, err := gexec.Start(command, buffer, buffer)
					Expect(err).NotTo(HaveOccurred())

					Eventually(session).Should(gexec.Exit(1), func() string { return string(buffer.Contents()) })
					Expect(string(buffer.Contents())).To(ContainSubstring("Error: required flag(s) \"package-file\" not set"))
				})
			})

			context("when the buildpack file does not exist", func() {
				it("prints an error and exits non-zero", func() {
					command := exec.Command(
						path,
						"update-buildpack",
						"--buildpack-file", "/no/such/file",
						"--package-file", filepath.Join(buildpackDir, "package.toml"),
					)

					buffer := gbytes.NewBuffer()
					session, err := gexec.Start(command, buffer, buffer)
					Expect(err).NotTo(HaveOccurred())

					Eventually(session).Should(gexec.Exit(1), func() string { return string(buffer.Contents()) })
					Expect(string(buffer.Contents())).To(ContainSubstring("failed to execute: failed to open buildpack config file: open /no/such/file: no such file or directory"))
				})
			})

			context("when the package file does not exist", func() {
				it("prints an error and exits non-zero", func() {
					command := exec.Command(
						path,
						"update-buildpack",
						"--buildpack-file", filepath.Join(buildpackDir, "buildpack.toml"),
						"--package-file", "/no/such/file",
					)

					buffer := gbytes.NewBuffer()
					session, err := gexec.Start(command, buffer, buffer)
					Expect(err).NotTo(HaveOccurred())

					Eventually(session).Should(gexec.Exit(1), func() string { return string(buffer.Contents()) })
					Expect(string(buffer.Contents())).To(ContainSubstring("failed to execute: failed to open package config file: open /no/such/file: no such file or directory"))
				})
			})

			context("when the buildpackage ID cannot be retrieved", func() {
				it.Before(func() {
					err := os.WriteFile(filepath.Join(buildpackDir, "buildpack.toml"), []byte(`
						api = "0.2"

						[buildpack]
							id = "some-composite-buildpack"
							name = "Some Composite Buildpack"
							version = "some-composite-buildpack-version"

						[metadata]
							include-files = ["buildpack.toml"]

						[[order]]
							[[order.group]]
								id = "some-repository/nonexistent-labels-id"
								version = "0.2.0"
					`), 0600)
					Expect(err).NotTo(HaveOccurred())

					err = os.WriteFile(filepath.Join(buildpackDir, "package.toml"), bytes.ReplaceAll([]byte(`
						[buildpack]
						uri = "build/buildpack.tgz"

						[[dependencies]]
						uri = "docker://REGISTRY-URI/some-repository/nonexistent-labels-id:0.2.0"
					`), []byte(`REGISTRY-URI`), []byte(strings.TrimPrefix(server.URL, "http://"))), 0600)
					Expect(err).NotTo(HaveOccurred())
				})

				it("prints an error and exits non-zero", func() {
					command := exec.Command(
						path,
						"update-buildpack",
						"--buildpack-file", filepath.Join(buildpackDir, "buildpack.toml"),
						"--package-file", filepath.Join(buildpackDir, "package.toml"),
					)

					buffer := gbytes.NewBuffer()
					session, err := gexec.Start(command, buffer, buffer)
					Expect(err).NotTo(HaveOccurred())

					Eventually(session).Should(gexec.Exit(1), func() string { return string(buffer.Contents()) })
					Expect(string(buffer.Contents())).To(MatchRegexp(`failed to get buildpackage ID for \d+\.\d+\.\d+\.\d+\:\d+\/some\-repository\/nonexistent\-labels\-id\:0\.2\.0\:`))
					Expect(string(buffer.Contents())).To(ContainSubstring("unexpected status code 400 Bad Request"))
				})
			})

			context("when the latest image on CNB registry cannot be found", func() {
				it.Before(func() {
					err := os.WriteFile(filepath.Join(buildpackDir, "buildpack.toml"), []byte(`
						api = "0.2"

						[buildpack]
							id = "some-composite-buildpack"
							name = "Some Composite Buildpack"
							version = "some-composite-buildpack-version"

						[metadata]
							include-files = ["buildpack.toml"]

						[[order]]
							[[order.group]]
								id = "some-repository/not-on-registry"
								version = "0.2.0"
					`), 0600)
					Expect(err).NotTo(HaveOccurred())

					err = os.WriteFile(filepath.Join(buildpackDir, "package.toml"), []byte(`
						[buildpack]
						uri = "build/buildpack.tgz"

						[[dependencies]]
						uri = "urn:cnb:registry:some-repository/not-on-registry@0.2.0"
					`), 0600)
					Expect(err).NotTo(HaveOccurred())
				})

				it("prints an error and exits non-zero", func() {
					command := exec.Command(
						path,
						"update-buildpack",
						"--buildpack-file", filepath.Join(buildpackDir, "buildpack.toml"),
						"--package-file", filepath.Join(buildpackDir, "package.toml"),
					)

					buffer := gbytes.NewBuffer()
					session, err := gexec.Start(command, buffer, buffer)
					Expect(err).NotTo(HaveOccurred())

					Eventually(session).Should(gexec.Exit(1), func() string { return string(buffer.Contents()) })
					Expect(string(buffer.Contents())).To(ContainSubstring("unexpected response status: 404 Not Found"))
				})
			})

			context("when the buildpack file cannot be overwritten", func() {
				it.Before(func() {
					Expect(os.Chmod(filepath.Join(buildpackDir, "buildpack.toml"), 0400)).To(Succeed())
				})

				it("prints an error and exits non-zero", func() {
					command := exec.Command(
						path,
						"update-buildpack",
						"--buildpack-file", filepath.Join(buildpackDir, "buildpack.toml"),
						"--package-file", filepath.Join(buildpackDir, "package.toml"),
					)

					buffer := gbytes.NewBuffer()
					session, err := gexec.Start(command, buffer, buffer)
					Expect(err).NotTo(HaveOccurred())

					Eventually(session).Should(gexec.Exit(1), func() string { return string(buffer.Contents()) })
					Expect(string(buffer.Contents())).To(ContainSubstring("failed to open buildpack config"))
				})
			})

			context("when the package file cannot be overwritten", func() {
				it.Before(func() {
					Expect(os.Chmod(filepath.Join(buildpackDir, "package.toml"), 0400)).To(Succeed())
				})

				it("prints an error and exits non-zero", func() {
					command := exec.Command(
						path,
						"update-buildpack",
						"--buildpack-file", filepath.Join(buildpackDir, "buildpack.toml"),
						"--package-file", filepath.Join(buildpackDir, "package.toml"),
					)

					buffer := gbytes.NewBuffer()
					session, err := gexec.Start(command, buffer, buffer)
					Expect(err).NotTo(HaveOccurred())

					Eventually(session).Should(gexec.Exit(1), func() string { return string(buffer.Contents()) })
					Expect(string(buffer.Contents())).To(ContainSubstring("failed to open package config"))
				})
			})

			context("--patch-only flag is set and versions cannot be parsed", func() {
				it.Before(func() {
					err := os.WriteFile(filepath.Join(buildpackDir, "buildpack.toml"), []byte(`
						api = "0.2"

						[buildpack]
							id = "some-composite-buildpack"
							name = "Some Composite Buildpack"
							version = "some-composite-buildpack-version"

						[metadata]
							include-files = ["buildpack.toml"]

						[[order]]
							[[order.group]]
						    id = "paketo-buildpacks/go-dist"
						    version = "bad-version"
					`), 0600)
					Expect(err).NotTo(HaveOccurred())

					err = os.WriteFile(filepath.Join(buildpackDir, "package.toml"), bytes.ReplaceAll([]byte(`
						[buildpack]
						uri = "build/buildpack.tgz"

						[[dependencies]]
				    uri = "docker://REGISTRY-URI/paketo-buildpacks/go-dist:bad-version"
					`), []byte(`REGISTRY-URI`), []byte(strings.TrimPrefix(server.URL, "http://"))), 0600)
					Expect(err).NotTo(HaveOccurred())
				})
				it("returns an error", func() {
					command := exec.Command(
						path,
						"update-buildpack",
						"--buildpack-file", filepath.Join(buildpackDir, "buildpack.toml"),
						"--package-file", filepath.Join(buildpackDir, "package.toml"),
						"--patch-only",
					)

					buffer := gbytes.NewBuffer()
					session, err := gexec.Start(command, buffer, buffer)
					Expect(err).NotTo(HaveOccurred())

					Eventually(session).Should(gexec.Exit(1), func() string { return string(buffer.Contents()) })
					Expect(string(buffer.Contents())).To(ContainSubstring("version constraint ~bad-version is not a valid semantic version constraint: improper constraint: ~bad-version"))
				})
			})
		})
	})

	context("updating non-cnb registry image refs", func() {
		it.Before(func() {
			goRef, err := name.ParseReference("index.docker.io/paketobuildpacks/go-dist")
			Expect(err).ToNot(HaveOccurred())
			goImg, err := remote.Image(goRef)
			Expect(err).ToNot(HaveOccurred())

			nodeRef, err := name.ParseReference("paketobuildpacks/node-engine")
			Expect(err).ToNot(HaveOccurred())
			nodeImg, err := remote.Image(nodeRef)
			Expect(err).ToNot(HaveOccurred())

			rubyRef, err := name.ParseReference("paketobuildpacks/mri")
			Expect(err).ToNot(HaveOccurred())
			rubyImg, err := remote.Image(rubyRef)
			Expect(err).ToNot(HaveOccurred())

			goManifestPath := "/v2/paketo-buildpacks/go-dist/manifests/0.20.1"
			goConfigPath := fmt.Sprintf("/v2/paketo-buildpacks/go-dist/blobs/%s", mustConfigName(t, goImg))
			goManifestReqCount := 0
			nodeManifestPath := "/v2/paketobuildpacks/node-engine/manifests/0.1.0"
			nodeConfigPath := fmt.Sprintf("/v2/paketobuildpacks/node-engine/blobs/%s", mustConfigName(t, nodeImg))
			nodeManifestReqCount := 0
			rubyManifestPath := "/v2/paketobuildpacks/mri/manifests/0.2.0"
			rubyConfigPath := fmt.Sprintf("/v2/paketobuildpacks/mri/blobs/%s", mustConfigName(t, rubyImg))
			rubyManifestReqCount := 0

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method == http.MethodHead {
					http.Error(w, "NotFound", http.StatusNotFound)

					return
				}

				switch req.URL.Path {
				case "/v2/":
					w.WriteHeader(http.StatusOK)

				case "/v2/paketo-buildpacks/go-dist/tags/list":
					w.WriteHeader(http.StatusOK)
					_, err := fmt.Fprintln(w, `{
						  "tags": [
								"0.0.10",
								"0.20.1",
								"0.20.12",
								"0.21.0",
								"latest"
							]
					}`)
					Expect(err).NotTo(HaveOccurred())

				case "/v2/paketobuildpacks/node-engine/tags/list":
					w.WriteHeader(http.StatusOK)
					_, err := fmt.Fprintln(w, `{
						  "tags": [
								"0.0.10",
								"0.1.0",
								"0.20.2",
								"0.20.22"
							]
					}`)
					Expect(err).NotTo(HaveOccurred())

				case "/v2/paketobuildpacks/mri/tags/list":
					w.WriteHeader(http.StatusOK)
					_, err := fmt.Fprintln(w, `{
						  "tags": [
								"0.0.10",
								"0.1.0",
								"0.2.0",
								"latest"
							]
					}`)
					Expect(err).NotTo(HaveOccurred())

				case goConfigPath:
					if req.Method != http.MethodGet {
						t.Errorf("Method; got %v, want %v", req.Method, http.MethodGet)
					}
					_, _ = w.Write(mustRawConfigFile(t, goImg))

				case goManifestPath:
					goManifestReqCount++
					if req.Method != http.MethodGet {
						t.Errorf("Method; got %v, want %v", req.Method, http.MethodGet)
					}
					_, _ = w.Write(mustRawManifest(t, goImg))

				case nodeConfigPath:
					if req.Method != http.MethodGet {
						t.Errorf("Method; got %v, want %v", req.Method, http.MethodGet)
					}
					_, _ = w.Write(mustRawConfigFile(t, nodeImg))

				case nodeManifestPath:
					nodeManifestReqCount++
					if req.Method != http.MethodGet {
						t.Errorf("Method; got %v, want %v", req.Method, http.MethodGet)
					}
					_, _ = w.Write(mustRawManifest(t, nodeImg))

				case rubyConfigPath:
					if req.Method != http.MethodGet {
						t.Errorf("Method; got %v, want %v", req.Method, http.MethodGet)
					}
					_, _ = w.Write(mustRawConfigFile(t, rubyImg))

				case rubyManifestPath:
					rubyManifestReqCount++
					if req.Method != http.MethodGet {
						t.Errorf("Method; got %v, want %v", req.Method, http.MethodGet)
					}
					_, _ = w.Write(mustRawManifest(t, rubyImg))

				case "/v2/some-repository/error-buildpack-id/tags/list":
					w.WriteHeader(http.StatusTeapot)

				case "/v2/some-repository/nonexistent-labels-id/tags/list":
					w.WriteHeader(http.StatusOK)
					_, err := fmt.Fprintln(w, `{
						  "tags": [
								"0.1.0",
								"0.2.0",
								"latest"
							]
					}`)
					Expect(err).NotTo(HaveOccurred())

				case "/v2/some-repository/nonexistent-labels-id/manifests/0.2.0":
					w.WriteHeader(http.StatusBadRequest)

				default:
					t.Fatalf("unknown path: %s", req.URL.Path)
				}
			}))

			buildpackDir, err = os.MkdirTemp("", "")
			Expect(err).NotTo(HaveOccurred())

			err = os.WriteFile(filepath.Join(buildpackDir, "buildpack.toml"), []byte(`
				api = "0.2"

				[buildpack]
					id = "some-composite-buildpack"
					name = "Some Composite Buildpack"
					version = "some-composite-buildpack-version"

				[metadata]
					include-files = ["buildpack.toml"]

				[[order]]
					[[order.group]]
						id = "paketo-buildpacks/go-dist"
						version = "0.20.1"

					[[order.group]]
						id = "paketo-buildpacks/mri"
						version = "0.2.0"

				[[order]]
					[[order.group]]
						id = "paketo-buildpacks/node-engine"
						version = "0.1.0"
						optional = true

					[[order.group]]
						id = "paketo-buildpacks/go-dist"
						version = "0.20.1"
			`), 0600)
			Expect(err).NotTo(HaveOccurred())

			err = os.WriteFile(filepath.Join(buildpackDir, "package.toml"), bytes.ReplaceAll([]byte(`
				[buildpack]
				uri = "build/buildpack.tgz"

				[[dependencies]]
				uri = "docker://REGISTRY-URI/paketobuildpacks/mri:0.2.0"

				[[dependencies]]
				uri = "docker://REGISTRY-URI/paketo-buildpacks/go-dist:0.20.1"

				[[dependencies]]
				uri = "docker://REGISTRY-URI/paketobuildpacks/node-engine:0.1.0"
			`), []byte(`REGISTRY-URI`), []byte(strings.TrimPrefix(server.URL, "http://"))), 0600)
			Expect(err).NotTo(HaveOccurred())
		})

		it.After(func() {
			server.Close()
			Expect(os.RemoveAll(buildpackDir)).To(Succeed())
		})

		it("updates the buildpack.toml and package.toml files", func() {
			command := exec.Command(
				path,
				"update-buildpack",
				"--buildpack-file", filepath.Join(buildpackDir, "buildpack.toml"),
				"--package-file", filepath.Join(buildpackDir, "package.toml"),
				"--no-cnb-registry",
			)

			buffer := gbytes.NewBuffer()
			session, err := gexec.Start(command, buffer, buffer)
			Expect(err).NotTo(HaveOccurred())

			Eventually(session).Should(gexec.Exit(0), func() string { return string(buffer.Contents()) })

			buildpackContents, err := os.ReadFile(filepath.Join(buildpackDir, "buildpack.toml"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(buildpackContents)).To(MatchTOML(`
				api = "0.2"

				[buildpack]
					id = "some-composite-buildpack"
					name = "Some Composite Buildpack"
					version = "some-composite-buildpack-version"

				[metadata]
					include-files = ["buildpack.toml"]

				[[order]]
					[[order.group]]
						id = "paketo-buildpacks/go-dist"
						version = "0.21.0"

					[[order.group]]
						id = "paketo-buildpacks/mri"
						version = "0.2.0"

				[[order]]
					[[order.group]]
						id = "paketo-buildpacks/node-engine"
						version = "0.20.22"
						optional = true

					[[order.group]]
						id = "paketo-buildpacks/go-dist"
						version = "0.21.0"
			`))

			packageContents, err := os.ReadFile(filepath.Join(buildpackDir, "package.toml"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(packageContents)).To(MatchTOML(strings.ReplaceAll(`
				[buildpack]
				uri = "build/buildpack.tgz"

				[[dependencies]]
				uri = "docker://REGISTRY-URI/paketobuildpacks/mri:0.2.0"

				[[dependencies]]
				uri = "docker://REGISTRY-URI/paketo-buildpacks/go-dist:0.21.0"

				[[dependencies]]
				uri = "docker://REGISTRY-URI/paketobuildpacks/node-engine:0.20.22"
			`, "REGISTRY-URI", strings.TrimPrefix(server.URL, "http://"))))
		})

		context("the --patch-only flag is set", func() {
			it("updates ONLY patch-level changes in the buildpack.toml and package.toml files", func() {
				command := exec.Command(
					path,
					"update-buildpack",
					"--buildpack-file", filepath.Join(buildpackDir, "buildpack.toml"),
					"--package-file", filepath.Join(buildpackDir, "package.toml"),
					"--no-cnb-registry",
					"--patch-only",
				)

				buffer := gbytes.NewBuffer()
				session, err := gexec.Start(command, buffer, buffer)
				Expect(err).NotTo(HaveOccurred())

				Eventually(session).Should(gexec.Exit(0), func() string { return string(buffer.Contents()) })

				buildpackContents, err := os.ReadFile(filepath.Join(buildpackDir, "buildpack.toml"))
				Expect(err).NotTo(HaveOccurred())
				Expect(string(buildpackContents)).To(MatchTOML(`
				api = "0.2"

				[buildpack]
					id = "some-composite-buildpack"
					name = "Some Composite Buildpack"
					version = "some-composite-buildpack-version"

				[metadata]
					include-files = ["buildpack.toml"]

				[[order]]
					[[order.group]]
						id = "paketo-buildpacks/go-dist"
						version = "0.20.12"

					[[order.group]]
						id = "paketo-buildpacks/mri"
						version = "0.2.0"

				[[order]]
					[[order.group]]
						id = "paketo-buildpacks/node-engine"
						version = "0.1.0"
						optional = true

					[[order.group]]
						id = "paketo-buildpacks/go-dist"
						version = "0.20.12"
			`))

				packageContents, err := os.ReadFile(filepath.Join(buildpackDir, "package.toml"))
				Expect(err).NotTo(HaveOccurred())
				Expect(string(packageContents)).To(MatchTOML(strings.ReplaceAll(`
				[buildpack]
				uri = "build/buildpack.tgz"

				[[dependencies]]
				uri = "docker://REGISTRY-URI/paketobuildpacks/mri:0.2.0"

				[[dependencies]]
				uri = "docker://REGISTRY-URI/paketo-buildpacks/go-dist:0.20.12"

				[[dependencies]]
				uri = "docker://REGISTRY-URI/paketobuildpacks/node-engine:0.1.0"
			`, "REGISTRY-URI", strings.TrimPrefix(server.URL, "http://"))))
			})
		})

		context("failure cases", func() {
			context("when the latest image reference cannot be found", func() {
				it.Before(func() {
					err := os.WriteFile(filepath.Join(buildpackDir, "buildpack.toml"), []byte(`
						api = "0.2"

						[buildpack]
							id = "some-composite-buildpack"
							name = "Some Composite Buildpack"
							version = "some-composite-buildpack-version"

						[metadata]
							include-files = ["buildpack.toml"]

						[[order]]
							[[order.group]]
								id = "some-repository/error-buildpack-id"
								version = "0.20.1"
					`), 0600)
					Expect(err).NotTo(HaveOccurred())

					err = os.WriteFile(filepath.Join(buildpackDir, "package.toml"), bytes.ReplaceAll([]byte(`
						[buildpack]
						uri = "build/buildpack.tgz"

						[[dependencies]]
						uri = "docker://REGISTRY-URI/some-repository/error-buildpack-id:0.20.1"
					`), []byte(`REGISTRY-URI`), []byte(strings.TrimPrefix(server.URL, "http://"))), 0600)
					Expect(err).NotTo(HaveOccurred())
				})

				it("prints an error and exits non-zero", func() {
					command := exec.Command(
						path,
						"update-buildpack",
						"--buildpack-file", filepath.Join(buildpackDir, "buildpack.toml"),
						"--package-file", filepath.Join(buildpackDir, "package.toml"),
						"--no-cnb-registry",
					)

					buffer := gbytes.NewBuffer()
					session, err := gexec.Start(command, buffer, buffer)
					Expect(err).NotTo(HaveOccurred())

					Eventually(session).Should(gexec.Exit(1), func() string { return string(buffer.Contents()) })
					Expect(string(buffer.Contents())).To(ContainSubstring("failed to list tags"))
				})
			})

			context("when the buildpackage ID cannot be retrieved", func() {
				it.Before(func() {
					err := os.WriteFile(filepath.Join(buildpackDir, "buildpack.toml"), []byte(`
						api = "0.2"

						[buildpack]
							id = "some-composite-buildpack"
							name = "Some Composite Buildpack"
							version = "some-composite-buildpack-version"

						[metadata]
							include-files = ["buildpack.toml"]

						[[order]]
							[[order.group]]
								id = "some-repository/nonexistent-labels-id"
								version = "0.2.0"
					`), 0600)
					Expect(err).NotTo(HaveOccurred())

					err = os.WriteFile(filepath.Join(buildpackDir, "package.toml"), bytes.ReplaceAll([]byte(`
						[buildpack]
						uri = "build/buildpack.tgz"

						[[dependencies]]
						uri = "docker://REGISTRY-URI/some-repository/nonexistent-labels-id:0.2.0"
					`), []byte(`REGISTRY-URI`), []byte(strings.TrimPrefix(server.URL, "http://"))), 0600)
					Expect(err).NotTo(HaveOccurred())
				})

				it("prints an error and exits non-zero", func() {
					command := exec.Command(
						path,
						"update-buildpack",
						"--buildpack-file", filepath.Join(buildpackDir, "buildpack.toml"),
						"--package-file", filepath.Join(buildpackDir, "package.toml"),
						"--no-cnb-registry",
					)

					buffer := gbytes.NewBuffer()
					session, err := gexec.Start(command, buffer, buffer)
					Expect(err).NotTo(HaveOccurred())

					Eventually(session).Should(gexec.Exit(1), func() string { return string(buffer.Contents()) })
					Expect(string(buffer.Contents())).To(MatchRegexp(`failed to get buildpackage ID for \d+\.\d+\.\d+\.\d+\:\d+\/some\-repository\/nonexistent\-labels\-id\:0\.2\.0\:`))
					Expect(string(buffer.Contents())).To(ContainSubstring("unexpected status code 400 Bad Request"))
				})
			})

			context("--patch-only flag is set and versions cannot be parsed", func() {
				it.Before(func() {
					err := os.WriteFile(filepath.Join(buildpackDir, "buildpack.toml"), []byte(`
						api = "0.2"

						[buildpack]
							id = "some-composite-buildpack"
							name = "Some Composite Buildpack"
							version = "some-composite-buildpack-version"

						[metadata]
							include-files = ["buildpack.toml"]

						[[order]]
							[[order.group]]
						    id = "paketo-buildpacks/go-dist"
						    version = "bad-version"
					`), 0600)
					Expect(err).NotTo(HaveOccurred())

					err = os.WriteFile(filepath.Join(buildpackDir, "package.toml"), bytes.ReplaceAll([]byte(`
						[buildpack]
						uri = "build/buildpack.tgz"

						[[dependencies]]
				    uri = "docker://REGISTRY-URI/paketo-buildpacks/go-dist:bad-version"
					`), []byte(`REGISTRY-URI`), []byte(strings.TrimPrefix(server.URL, "http://"))), 0600)
					Expect(err).NotTo(HaveOccurred())
				})
				it("returns an error", func() {
					command := exec.Command(
						path,
						"update-buildpack",
						"--buildpack-file", filepath.Join(buildpackDir, "buildpack.toml"),
						"--package-file", filepath.Join(buildpackDir, "package.toml"),
						"--patch-only",
						"--no-cnb-registry",
					)

					buffer := gbytes.NewBuffer()
					session, err := gexec.Start(command, buffer, buffer)
					Expect(err).NotTo(HaveOccurred())

					Eventually(session).Should(gexec.Exit(1), func() string { return string(buffer.Contents()) })
					Expect(string(buffer.Contents())).To(ContainSubstring("version constraint ~bad-version is not a valid semantic version constraint: improper constraint: ~bad-version"))
				})
			})
		})
	})
}
