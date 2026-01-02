package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/containers/image/v5/copy"
	"github.com/containers/image/v5/docker"
	"github.com/containers/image/v5/oci/layout"
	"github.com/containers/image/v5/signature"
	"github.com/containers/image/v5/types"
	"gopkg.in/yaml.v3"

	buildapitypes "github.com/centos-automotive-suite/automotive-dev-operator/internal/buildapi"
	buildapiclient "github.com/centos-automotive-suite/automotive-dev-operator/internal/buildapi/client"
	progressbar "github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	serverURL              string
	imageBuildCfg          string
	manifest               string
	buildName              string
	distro                 string
	target                 string
	architecture           string
	exportFormat           string
	mode                   string
	automotiveImageBuilder string
	storageClass           string
	outputDir              string
	timeout                int
	waitForBuild           bool
	download               bool
	customDefs             []string
	followLogs             bool
	version                string
	aibExtraArgs           string
	aibOverrideArgs        string
	compressArtifacts      bool
	compressionAlgo        string
	authToken              string
	pushRepository         string
	registryURL            string
	registryUsername       string
	registryPassword       string

	containerPush  string
	buildDiskImage bool
	diskFormat     string
	exportOCI      string
	builderImage   string

	downloadFile string
)

func main() {
	rootCmd := &cobra.Command{
		Use:     "caib",
		Short:   "Cloud Automotive Image Builder",
		Version: version,
	}

	rootCmd.InitDefaultVersionFlag()
	rootCmd.SetVersionTemplate("caib version: {{.Version}}\n")

	// New subcommands
	buildBootcCmd := &cobra.Command{
		Use:   "build-bootc <manifest.aib.yml>",
		Short: "Build bootc container image with optional disk image",
		Args:  cobra.ExactArgs(1),
		Run:   runBuildBootc,
	}

	buildTraditionalCmd := &cobra.Command{
		Use:   "build-traditional <manifest.aib.yml>",
		Short: "Build traditional disk image (ostree or package-based)",
		Args:  cobra.ExactArgs(1),
		Run:   runBuildTraditional,
	}

	// Legacy build command (deprecated)
	buildCmd := &cobra.Command{
		Use:        "build",
		Short:      "Create an ImageBuild resource (deprecated, use build-bootc or build-traditional)",
		Run:        runBuild,
		Deprecated: "use 'build-bootc' or 'build-traditional' instead",
	}

	downloadCmd := &cobra.Command{
		Use:   "download",
		Short: "Download artifacts from a completed build",
		Run:   runDownload,
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List existing ImageBuilds",
		Run:   runList,
	}

	buildCmd.Flags().StringVar(&serverURL, "server", os.Getenv("CAIB_SERVER"), "REST API server base URL (e.g. https://api.example)")
	buildCmd.Flags().StringVar(&authToken, "token", os.Getenv("CAIB_TOKEN"), "Bearer token for authentication (e.g., OpenShift access token)")
	buildCmd.Flags().StringVar(&imageBuildCfg, "config", "", "path to ImageBuild YAML configuration file")
	buildCmd.Flags().StringVar(&manifest, "manifest", "", "path to manifest YAML file for the build")
	buildCmd.Flags().StringVar(&buildName, "name", "", "name for the ImageBuild")
	buildCmd.Flags().StringVar(&distro, "distro", "autosd", "distribution to build")
	buildCmd.Flags().StringVar(&target, "target", "qemu", "target platform (qemu, etc)")
	buildCmd.Flags().StringVar(&architecture, "arch", "arm64", "architecture (amd64, arm64)")
	buildCmd.Flags().StringVar(&exportFormat, "format", "qcow2", "disk image format (qcow2, raw, etc)")
	buildCmd.Flags().StringVar(&mode, "mode", "bootc", "build mode (bootc, image, package)")
	buildCmd.Flags().StringVar(&automotiveImageBuilder, "automotive-image-builder", "quay.io/centos-sig-automotive/automotive-image-builder:1.0.0", "container image for automotive-image-builder")
	buildCmd.Flags().StringVar(&storageClass, "storage-class", "", "storage class to use for build workspace PVC")
	buildCmd.Flags().IntVar(&timeout, "timeout", 60, "timeout in minutes when waiting for build completion")
	buildCmd.Flags().BoolVarP(&waitForBuild, "wait", "w", false, "wait for the build to complete")
	buildCmd.Flags().BoolVarP(&download, "download", "d", false, "automatically download artifacts when build completes")
	buildCmd.Flags().BoolVar(&compressArtifacts, "compress", true, "compress directory artifacts (tar.gz). For directories, server always compresses.")
	buildCmd.Flags().BoolVarP(&followLogs, "follow", "f", false, "follow logs of the build")
	buildCmd.Flags().StringArrayVar(&customDefs, "define", []string{}, "Custom definition in KEY=VALUE format (can be specified multiple times)")
	buildCmd.Flags().StringVar(&aibExtraArgs, "aib-args", "", "extra arguments passed to automotive-image-builder (space-separated)")
	buildCmd.Flags().StringVar(&aibOverrideArgs, "override", "", "override arguments passed as-is to automotive-image-builder")
	buildCmd.Flags().StringVar(&compressionAlgo, "compression", "gzip", "artifact compression algorithm (lz4|gzip)")
	buildCmd.Flags().StringVar(&pushRepository, "push", "", "push artifact to OCI registry (e.g., quay.io/myorg/myimage:tag)")
	buildCmd.Flags().StringVar(&registryURL, "registry-url", "", "registry URL for authentication (optional, extracted from --push if not set)")
	buildCmd.Flags().StringVar(&registryUsername, "registry-username", "", "registry username for push authentication")
	buildCmd.Flags().StringVar(&registryPassword, "registry-password", "", "registry password for push authentication")
	_ = buildCmd.MarkFlagRequired("arch")

	downloadCmd.Flags().StringVar(&serverURL, "server", os.Getenv("CAIB_SERVER"), "REST API server base URL (e.g. https://api.example)")
	downloadCmd.Flags().StringVar(&authToken, "token", os.Getenv("CAIB_TOKEN"), "Bearer token for authentication (e.g., OpenShift access token)")
	downloadCmd.Flags().StringVar(&buildName, "name", "", "name of the ImageBuild")
	downloadCmd.Flags().StringVar(&outputDir, "output-dir", "./output", "directory to save artifacts")
	downloadCmd.MarkFlagRequired("name")
	downloadCmd.Flags().BoolVar(&compressArtifacts, "compress", true, "compress directory artifacts (tar.gz). For directories, server always compresses.")

	listCmd.Flags().StringVar(&serverURL, "server", os.Getenv("CAIB_SERVER"), "REST API server base URL (e.g. https://api.example)")
	listCmd.Flags().StringVar(&authToken, "token", os.Getenv("CAIB_TOKEN"), "Bearer token for authentication (e.g., OpenShift access token)")

	// build-bootc flags
	buildBootcCmd.Flags().StringVar(&serverURL, "server", os.Getenv("CAIB_SERVER"), "REST API server base URL")
	buildBootcCmd.Flags().StringVar(&authToken, "token", os.Getenv("CAIB_TOKEN"), "Bearer token for authentication")
	buildBootcCmd.Flags().StringVar(&buildName, "name", "", "name for the ImageBuild")
	buildBootcCmd.Flags().StringVar(&distro, "distro", "autosd", "distribution to build")
	buildBootcCmd.Flags().StringVar(&target, "target", "qemu", "target platform")
	buildBootcCmd.Flags().StringVar(&architecture, "arch", "arm64", "architecture (amd64, arm64)")
	buildBootcCmd.Flags().StringVar(&containerPush, "push", "", "push bootc container to registry (e.g., quay.io/org/image:tag)")
	buildBootcCmd.Flags().BoolVar(&buildDiskImage, "build-disk-image", false, "also build disk image from container")
	buildBootcCmd.Flags().StringVar(&diskFormat, "format", "qcow2", "disk image format (qcow2, raw, simg)")
	buildBootcCmd.Flags().StringVar(&compressionAlgo, "compress", "gzip", "compression algorithm (gzip, lz4, xz)")
	buildBootcCmd.Flags().StringVar(&exportOCI, "export-oci", "", "push disk image as OCI artifact to registry")
	buildBootcCmd.Flags().StringVar(&registryUsername, "registry-username", "", "registry username for push/export (or set REGISTRY_USERNAME env var)")
	buildBootcCmd.Flags().StringVar(&registryPassword, "registry-password", "", "registry password for push/export (or set REGISTRY_PASSWORD env var, or use docker/podman auth)")
	buildBootcCmd.Flags().StringVar(&automotiveImageBuilder, "automotive-image-builder", "quay.io/centos-sig-automotive/automotive-image-builder:latest", "container image for aib")
	buildBootcCmd.Flags().StringVar(&builderImage, "builder-image", "", "custom aib-build container")
	buildBootcCmd.Flags().StringVar(&storageClass, "storage-class", "", "storage class for build workspace PVC")
	buildBootcCmd.Flags().StringArrayVar(&customDefs, "define", []string{}, "custom definition KEY=VALUE")
	buildBootcCmd.Flags().IntVar(&timeout, "timeout", 60, "timeout in minutes")
	buildBootcCmd.Flags().BoolVarP(&waitForBuild, "wait", "w", false, "wait for build to complete")
	buildBootcCmd.Flags().BoolVarP(&followLogs, "follow", "f", false, "follow build logs")
	buildBootcCmd.Flags().StringVar(&downloadFile, "download", "", "download disk image artifact to local file (requires --export-oci)")
	_ = buildBootcCmd.MarkFlagRequired("name")
	_ = buildBootcCmd.MarkFlagRequired("arch")

	// build-traditional flags
	buildTraditionalCmd.Flags().StringVar(&serverURL, "server", os.Getenv("CAIB_SERVER"), "REST API server base URL")
	buildTraditionalCmd.Flags().StringVar(&authToken, "token", os.Getenv("CAIB_TOKEN"), "Bearer token for authentication")
	buildTraditionalCmd.Flags().StringVar(&buildName, "name", "", "name for the ImageBuild")
	buildTraditionalCmd.Flags().StringVar(&distro, "distro", "autosd", "distribution to build")
	buildTraditionalCmd.Flags().StringVar(&target, "target", "qemu", "target platform")
	buildTraditionalCmd.Flags().StringVar(&architecture, "arch", "arm64", "architecture (amd64, arm64)")
	buildTraditionalCmd.Flags().StringVar(&mode, "mode", "image", "traditional mode (image, package)")
	buildTraditionalCmd.Flags().StringVar(&exportFormat, "export", "qcow2", "export format (qcow2, raw, simg)")
	buildTraditionalCmd.Flags().StringVar(&compressionAlgo, "compress", "gzip", "compression algorithm (gzip, lz4, xz)")
	buildTraditionalCmd.Flags().StringVar(&downloadFile, "download", "", "download artifact to local file")
	buildTraditionalCmd.Flags().StringVar(&exportOCI, "push", "", "push disk image as OCI artifact to registry")
	buildTraditionalCmd.Flags().StringVar(&registryUsername, "registry-username", "", "registry username for push (or set REGISTRY_USERNAME env var)")
	buildTraditionalCmd.Flags().StringVar(&registryPassword, "registry-password", "", "registry password for push (or set REGISTRY_PASSWORD env var, or use docker/podman auth)")
	buildTraditionalCmd.Flags().StringVar(&automotiveImageBuilder, "automotive-image-builder", "quay.io/centos-sig-automotive/automotive-image-builder:latest", "container image for aib")
	buildTraditionalCmd.Flags().StringVar(&storageClass, "storage-class", "", "storage class for build workspace PVC")
	buildTraditionalCmd.Flags().StringArrayVar(&customDefs, "define", []string{}, "custom definition KEY=VALUE")
	buildTraditionalCmd.Flags().IntVar(&timeout, "timeout", 60, "timeout in minutes")
	buildTraditionalCmd.Flags().BoolVarP(&waitForBuild, "wait", "w", false, "wait for build to complete")
	buildTraditionalCmd.Flags().BoolVarP(&followLogs, "follow", "f", false, "follow build logs")
	_ = buildTraditionalCmd.MarkFlagRequired("name")
	_ = buildTraditionalCmd.MarkFlagRequired("arch")

	rootCmd.AddCommand(buildBootcCmd, buildTraditionalCmd, buildCmd, downloadCmd, listCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runBuildBootc(cmd *cobra.Command, args []string) {
	ctx := context.Background()
	manifest = args[0]

	if serverURL == "" {
		handleError(fmt.Errorf("--server is required"))
	}
	if buildName == "" {
		handleError(fmt.Errorf("--name is required"))
	}
	if downloadFile != "" && exportOCI == "" {
		handleError(fmt.Errorf("--download requires --export-oci (the disk image must be pushed to a registry first)"))
	}

	if strings.TrimSpace(authToken) == "" {
		if tok, err := loadTokenFromKubeconfig(); err == nil && strings.TrimSpace(tok) != "" {
			authToken = tok
		}
	}

	var opts []buildapiclient.Option
	if strings.TrimSpace(authToken) != "" {
		opts = append(opts, buildapiclient.WithAuthToken(strings.TrimSpace(authToken)))
	}
	api, err := buildapiclient.New(serverURL, opts...)
	if err != nil {
		handleError(err)
	}

	manifestBytes, err := os.ReadFile(manifest)
	if err != nil {
		handleError(fmt.Errorf("error reading manifest: %w", err))
	}

	// Extract registry URL from push if not empty
	effectiveRegistryURL := ""
	if containerPush != "" || exportOCI != "" {
		// Try environment variables if command line flags are empty
		if registryUsername == "" {
			registryUsername = os.Getenv("REGISTRY_USERNAME")
		}
		if registryPassword == "" {
			registryPassword = os.Getenv("REGISTRY_PASSWORD")
		}

		// Note: Docker/Podman auth files will be tried as fallback in pullOCIArtifact
		if registryUsername == "" || registryPassword == "" {
			fmt.Println("Warning: No registry credentials provided via flags or environment variables.")
			fmt.Println("Will attempt to use Docker/Podman auth files as fallback.")
		}
		pushTarget := containerPush
		if pushTarget == "" {
			pushTarget = exportOCI
		}
		parts := strings.SplitN(pushTarget, "/", 2)
		if len(parts) > 0 && strings.Contains(parts[0], ".") {
			effectiveRegistryURL = parts[0]
		} else {
			effectiveRegistryURL = "docker.io"
		}
	}

	req := buildapitypes.BuildRequest{
		Name:                   buildName,
		Manifest:               string(manifestBytes),
		ManifestFileName:       filepath.Base(manifest),
		Distro:                 buildapitypes.Distro(distro),
		Target:                 buildapitypes.Target(target),
		Architecture:           buildapitypes.Architecture(architecture),
		ExportFormat:           buildapitypes.ExportFormat(diskFormat),
		Mode:                   buildapitypes.ModeBootc,
		AutomotiveImageBuilder: automotiveImageBuilder,
		StorageClass:           storageClass,
		CustomDefs:             customDefs,
		Compression:            compressionAlgo,
		ContainerPush:          containerPush,
		BuildDiskImage:         buildDiskImage,
		ExportOCI:              exportOCI,
		BuilderImage:           builderImage,
	}

	if effectiveRegistryURL != "" {
		req.RegistryCredentials = &buildapitypes.RegistryCredentials{
			Enabled:     true,
			AuthType:    "username-password",
			RegistryURL: effectiveRegistryURL,
			Username:    registryUsername,
			Password:    registryPassword,
		}
	}

	resp, err := api.CreateBuild(ctx, req)
	if err != nil {
		handleError(err)
	}
	fmt.Printf("Build %s accepted: %s - %s\n", resp.Name, resp.Phase, resp.Message)

	// Handle local file uploads if needed
	localRefs, err := findLocalFileReferences(string(manifestBytes))
	if err != nil {
		handleError(fmt.Errorf("manifest file reference error: %w", err))
	}
	if len(localRefs) > 0 {
		handleFileUploads(ctx, api, resp.Name, localRefs)
	}

	if waitForBuild || followLogs || downloadFile != "" {
		waitForBuildCompletion(ctx, api, resp.Name, "")
	}

	if downloadFile != "" {
		if err := pullOCIArtifact(exportOCI, downloadFile, registryUsername, registryPassword); err != nil {
			handleError(fmt.Errorf("failed to download OCI artifact: %w", err))
		}
	}
}

func pullOCIArtifact(ociRef, destPath, username, password string) error {
	fmt.Printf("Pulling OCI artifact %s to %s\n", ociRef, destPath)

	// Ensure output directory exists
	destDir := filepath.Dir(destPath)
	if destDir != "" && destDir != "." {
		if err := os.MkdirAll(destDir, 0755); err != nil {
			return fmt.Errorf("create output dir: %w", err)
		}
	}

	ctx := context.Background()

	// Set up system context with authentication
	systemCtx := &types.SystemContext{}
	if username != "" && password != "" {
		fmt.Printf("Using provided username/password credentials\n")
		systemCtx.DockerAuthConfig = &types.DockerAuthConfig{
			Username: username,
			Password: password,
		}
	} else {
		fmt.Printf("No explicit credentials provided, will use Docker/Podman auth files if available\n")
		// containers/image will automatically use:
		// - $HOME/.docker/config.json
		// - $XDG_RUNTIME_DIR/containers/auth.json
		// - /run/containers/$UID/auth.json
		// - $HOME/.config/containers/auth.json
	}

	// Set up policy context (allow all)
	policyCtx, err := signature.NewPolicyContext(&signature.Policy{Default: []signature.PolicyRequirement{signature.NewPRInsecureAcceptAnything()}})
	if err != nil {
		return fmt.Errorf("create policy context: %w", err)
	}

	// Source: docker registry reference
	srcRef, err := docker.ParseReference("//" + ociRef)
	if err != nil {
		return fmt.Errorf("parse source reference: %w", err)
	}

	// Create temporary directory for OCI layout
	tempDir, err := os.MkdirTemp("", "oci-pull-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Destination: local OCI layout
	destRef, err := layout.ParseReference(tempDir + ":latest")
	if err != nil {
		return fmt.Errorf("parse destination reference: %w", err)
	}

	// Copy the image from registry to local OCI layout
	fmt.Printf("Downloading OCI artifact...")
	_, err = copy.Image(ctx, policyCtx, destRef, srcRef, &copy.Options{
		ReportWriter:   os.Stdout,
		SourceCtx:      systemCtx,
		DestinationCtx: systemCtx,
	})
	if err != nil {
		return fmt.Errorf("copy image: %w", err)
	}

	fmt.Printf("\nExtracting artifact to %s\n", destPath)

	// Extract the artifact blob to the destination file
	if err := extractOCIArtifactBlob(tempDir, destPath); err != nil {
		return fmt.Errorf("extract artifact: %w", err)
	}

	fmt.Printf("Downloaded to %s\n", destPath)
	return nil
}

func extractOCIArtifactBlob(ociLayoutPath, destPath string) error {
	// Read the index.json to find the manifest
	indexPath := filepath.Join(ociLayoutPath, "index.json")
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		return fmt.Errorf("read index.json: %w", err)
	}

	var index struct {
		Manifests []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(indexData, &index); err != nil {
		return fmt.Errorf("parse index.json: %w", err)
	}

	if len(index.Manifests) == 0 {
		return fmt.Errorf("no manifests found in index")
	}

	// Get the manifest digest and read the manifest
	manifestDigest := strings.TrimPrefix(index.Manifests[0].Digest, "sha256:")
	manifestPath := filepath.Join(ociLayoutPath, "blobs", "sha256", manifestDigest)
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}

	var manifest struct {
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}

	if len(manifest.Layers) == 0 {
		return fmt.Errorf("no layers found in manifest")
	}

	// Extract the first layer (should contain the disk image)
	layerDigest := strings.TrimPrefix(manifest.Layers[0].Digest, "sha256:")
	layerPath := filepath.Join(ociLayoutPath, "blobs", "sha256", layerDigest)

	// Copy the layer blob to destination
	src, err := os.Open(layerPath)
	if err != nil {
		return fmt.Errorf("open layer blob: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create destination file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copy layer blob: %w", err)
	}

	return nil
}

func runBuildTraditional(cmd *cobra.Command, args []string) {
	ctx := context.Background()
	manifest = args[0]

	if serverURL == "" {
		handleError(fmt.Errorf("--server is required"))
	}
	if buildName == "" {
		handleError(fmt.Errorf("--name is required"))
	}

	if strings.TrimSpace(authToken) == "" {
		if tok, err := loadTokenFromKubeconfig(); err == nil && strings.TrimSpace(tok) != "" {
			authToken = tok
		}
	}

	var opts []buildapiclient.Option
	if strings.TrimSpace(authToken) != "" {
		opts = append(opts, buildapiclient.WithAuthToken(strings.TrimSpace(authToken)))
	}
	api, err := buildapiclient.New(serverURL, opts...)
	if err != nil {
		handleError(err)
	}

	manifestBytes, err := os.ReadFile(manifest)
	if err != nil {
		handleError(fmt.Errorf("error reading manifest: %w", err))
	}

	// Validate mode
	parsedMode := buildapitypes.ModeImage
	if mode == "package" {
		parsedMode = buildapitypes.ModePackage
	}

	effectiveRegistryURL := ""
	if exportOCI != "" {
		// Try environment variables if command line flags are empty
		if registryUsername == "" {
			registryUsername = os.Getenv("REGISTRY_USERNAME")
		}
		if registryPassword == "" {
			registryPassword = os.Getenv("REGISTRY_PASSWORD")
		}

		// Note: Docker/Podman auth files will be tried as fallback
		if registryUsername == "" || registryPassword == "" {
			fmt.Println("Warning: No registry credentials provided via flags or environment variables.")
			fmt.Println("Will attempt to use Docker/Podman auth files as fallback.")
		}
		parts := strings.SplitN(exportOCI, "/", 2)
		if len(parts) > 0 && strings.Contains(parts[0], ".") {
			effectiveRegistryURL = parts[0]
		} else {
			effectiveRegistryURL = "docker.io"
		}
	}

	req := buildapitypes.BuildRequest{
		Name:                   buildName,
		Manifest:               string(manifestBytes),
		ManifestFileName:       filepath.Base(manifest),
		Distro:                 buildapitypes.Distro(distro),
		Target:                 buildapitypes.Target(target),
		Architecture:           buildapitypes.Architecture(architecture),
		ExportFormat:           buildapitypes.ExportFormat(exportFormat),
		Mode:                   parsedMode,
		AutomotiveImageBuilder: automotiveImageBuilder,
		StorageClass:           storageClass,
		CustomDefs:             customDefs,
		Compression:            compressionAlgo,
		ServeArtifact:          downloadFile != "",
		ExportOCI:              exportOCI,
	}

	if effectiveRegistryURL != "" {
		req.RegistryCredentials = &buildapitypes.RegistryCredentials{
			Enabled:     true,
			AuthType:    "username-password",
			RegistryURL: effectiveRegistryURL,
			Username:    registryUsername,
			Password:    registryPassword,
		}
	}

	resp, err := api.CreateBuild(ctx, req)
	if err != nil {
		handleError(err)
	}
	fmt.Printf("Build %s accepted: %s - %s\n", resp.Name, resp.Phase, resp.Message)

	// Handle local file uploads if needed
	localRefs, err := findLocalFileReferences(string(manifestBytes))
	if err != nil {
		handleError(fmt.Errorf("manifest file reference error: %w", err))
	}
	if len(localRefs) > 0 {
		handleFileUploads(ctx, api, resp.Name, localRefs)
	}

	if waitForBuild || followLogs || downloadFile != "" {
		waitForBuildCompletion(ctx, api, resp.Name, downloadFile)
	}
}

func handleFileUploads(ctx context.Context, api *buildapiclient.Client, buildName string, localRefs []map[string]string) {
	for _, ref := range localRefs {
		if _, err := os.Stat(ref["source_path"]); err != nil {
			handleError(fmt.Errorf("referenced file %s does not exist: %w", ref["source_path"], err))
		}
	}

	fmt.Println("Waiting for upload server to be ready...")
	readyCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	for {
		if err := readyCtx.Err(); err != nil {
			handleError(fmt.Errorf("timed out waiting for upload server to be ready"))
		}
		reqCtx, c := context.WithTimeout(ctx, 15*time.Second)
		st, err := api.GetBuild(reqCtx, buildName)
		c()
		if err == nil {
			if st.Phase == "Uploading" {
				break
			}
			if st.Phase == "Failed" {
				handleError(fmt.Errorf("build failed while waiting for upload server: %s", st.Message))
			}
		}
		time.Sleep(3 * time.Second)
	}

	uploads := make([]buildapiclient.Upload, 0, len(localRefs))
	for _, ref := range localRefs {
		uploads = append(uploads, buildapiclient.Upload{SourcePath: ref["source_path"], DestPath: ref["source_path"]})
	}

	uploadDeadline := time.Now().Add(10 * time.Minute)
	for {
		if err := api.UploadFiles(ctx, buildName, uploads); err != nil {
			lower := strings.ToLower(err.Error())
			if time.Now().After(uploadDeadline) {
				handleError(fmt.Errorf("upload files failed: %w", err))
			}
			if strings.Contains(lower, "503") || strings.Contains(lower, "service unavailable") || strings.Contains(lower, "upload pod not ready") {
				fmt.Println("Upload server not ready yet. Retrying...")
				time.Sleep(5 * time.Second)
				continue
			}
			handleError(fmt.Errorf("upload files failed: %w", err))
		}
		break
	}
	fmt.Println("Local files uploaded. Build will proceed.")
}

func waitForBuildCompletion(ctx context.Context, api *buildapiclient.Client, name, downloadTo string) {
	fmt.Println("Waiting for build to complete...")
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Minute)
	defer cancel()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	userFollowRequested := followLogs
	var lastPhase, lastMessage string
	logFollowWarned := false

	logClient := &http.Client{
		Timeout: 10 * time.Minute,
		Transport: &http.Transport{
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       2 * time.Minute,
		},
	}

	for {
		select {
		case <-timeoutCtx.Done():
			handleError(fmt.Errorf("timed out waiting for build"))
		case <-ticker.C:
			if followLogs {
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(serverURL, "/")+"/v1/builds/"+url.PathEscape(name)+"/logs?follow=1", nil)
				if strings.TrimSpace(authToken) != "" {
					req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(authToken))
				}
				resp, err := logClient.Do(req)
				if err == nil && resp.StatusCode == http.StatusOK {
					fmt.Println("Streaming logs...")
					io.Copy(os.Stdout, resp.Body)
					resp.Body.Close()
					followLogs = userFollowRequested
				} else if resp != nil {
					body, _ := io.ReadAll(resp.Body)
					msg := strings.TrimSpace(string(body))
					if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout {
						if !logFollowWarned {
							fmt.Println("log stream not ready (HTTP", resp.StatusCode, "). Retrying...")
							logFollowWarned = true
						}
					} else {
						if msg != "" {
							fmt.Printf("log stream error (%d): %s\n", resp.StatusCode, msg)
						} else {
							fmt.Printf("log stream error: HTTP %d\n", resp.StatusCode)
						}
						followLogs = false
					}
					resp.Body.Close()
				}
			}
			reqCtx, cancelReq := context.WithTimeout(ctx, 2*time.Minute)
			st, err := api.GetBuild(reqCtx, name)
			cancelReq()
			if err != nil {
				fmt.Printf("status check failed: %v\n", err)
				continue
			}
			if !userFollowRequested {
				if st.Phase != lastPhase || st.Message != lastMessage {
					fmt.Printf("status: %s - %s\n", st.Phase, st.Message)
					lastPhase = st.Phase
					lastMessage = st.Message
				}
			}
			if st.Phase == "Completed" {
				if downloadTo != "" {
					outDir := filepath.Dir(downloadTo)
					if outDir == "" || outDir == "." {
						outDir = "./output"
					}
					if err := downloadArtifactViaAPI(ctx, serverURL, name, outDir); err != nil {
						fmt.Printf("Download failed: %v\n", err)
					}
				}
				return
			}
			if st.Phase == "Failed" {
				handleError(fmt.Errorf("build failed: %s", st.Message))
			}
		}
	}
}

func runBuild(cmd *cobra.Command, args []string) {
	ctx := context.Background()

	if err := validateBuildRequirements(); err != nil {
		handleError(err)
	}

	if serverURL == "" {
		handleError(fmt.Errorf("--server is required"))
	}

	if serverURL != "" {
		if strings.TrimSpace(authToken) == "" {
			if tok, err := loadTokenFromKubeconfig(); err == nil && strings.TrimSpace(tok) != "" {
				authToken = tok
			}
		}
		var opts []buildapiclient.Option
		if strings.TrimSpace(authToken) != "" {
			opts = append(opts, buildapiclient.WithAuthToken(strings.TrimSpace(authToken)))
		}
		api, err := buildapiclient.New(serverURL, opts...)
		if err != nil {
			handleError(err)
		}

		manifestBytes, err := os.ReadFile(manifest)
		if err != nil {
			handleError(fmt.Errorf("error reading manifest: %w", err))
		}

		parsedDistro, err := buildapitypes.ParseDistro(distro)
		if err != nil {
			handleError(err)
		}
		parsedTarget, err := buildapitypes.ParseTarget(target)
		if err != nil {
			handleError(err)
		}
		parsedArch, err := buildapitypes.ParseArchitecture(architecture)
		if err != nil {
			handleError(err)
		}
		parsedExportFormat, err := buildapitypes.ParseExportFormat(exportFormat)
		if err != nil {
			handleError(err)
		}
		parsedMode, err := buildapitypes.ParseMode(mode)
		if err != nil {
			handleError(err)
		}

		var aibArgsArray []string
		var aibOverrideArray []string
		if strings.TrimSpace(aibExtraArgs) != "" {
			aibArgsArray = strings.Fields(aibExtraArgs)
		}
		if strings.TrimSpace(aibOverrideArgs) != "" {
			aibOverrideArray = strings.Fields(aibOverrideArgs)
		}

		req := buildapitypes.BuildRequest{
			Name:                   buildName,
			Manifest:               string(manifestBytes),
			ManifestFileName:       filepath.Base(manifest),
			Distro:                 parsedDistro,
			Target:                 parsedTarget,
			Architecture:           parsedArch,
			ExportFormat:           parsedExportFormat,
			Mode:                   parsedMode,
			AutomotiveImageBuilder: automotiveImageBuilder,
			StorageClass:           storageClass,
			CustomDefs:             customDefs,
			AIBExtraArgs:           aibArgsArray,
			AIBOverrideArgs:        aibOverrideArray,
			ServeArtifact:          download,
			Compression:            compressionAlgo,
			PushRepository:         pushRepository,
		}

		// Add registry credentials if push is configured
		if pushRepository != "" {
			if registryUsername == "" || registryPassword == "" {
				handleError(fmt.Errorf("--registry-username and --registry-password are required when --push is specified"))
			}
			// Extract registry URL from push repository if not explicitly provided
			effectiveRegistryURL := registryURL
			if effectiveRegistryURL == "" {
				// Extract registry from push repository (e.g., "quay.io/org/image:tag" -> "quay.io")
				parts := strings.SplitN(pushRepository, "/", 2)
				if len(parts) > 0 && strings.Contains(parts[0], ".") {
					effectiveRegistryURL = parts[0]
				} else {
					// Default to docker.io for short names
					effectiveRegistryURL = "docker.io"
				}
			}
			req.RegistryCredentials = &buildapitypes.RegistryCredentials{
				Enabled:     true,
				AuthType:    "username-password",
				RegistryURL: effectiveRegistryURL,
				Username:    registryUsername,
				Password:    registryPassword,
			}
		}

		resp, err := api.CreateBuild(ctx, req)
		if err != nil {
			handleError(err)
		}
		fmt.Printf("Build %s accepted: %s - %s\n", resp.Name, resp.Phase, resp.Message)
		// If manifest references local files, upload them via the API
		localRefs, err := findLocalFileReferences(string(manifestBytes))
		if err != nil {
			handleError(fmt.Errorf("manifest file reference error: %w", err))
		}
		if len(localRefs) > 0 {
			for _, ref := range localRefs {
				if _, err := os.Stat(ref["source_path"]); err != nil {
					handleError(fmt.Errorf("referenced file %s does not exist: %w", ref["source_path"], err))
				}
			}

			fmt.Println("Waiting for upload server to be ready...")
			readyCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cancel()
			for {
				if err := readyCtx.Err(); err != nil {
					handleError(fmt.Errorf("timed out waiting for upload server to be ready"))
				}
				reqCtx, c := context.WithTimeout(ctx, 15*time.Second)
				st, err := api.GetBuild(reqCtx, resp.Name)
				c()
				if err == nil {
					if st.Phase == "Uploading" {
						break
					}
					if st.Phase == "Failed" {
						handleError(fmt.Errorf("build failed while waiting for upload server: %s", st.Message))
					}
				}
				time.Sleep(3 * time.Second)
			}

			uploads := make([]buildapiclient.Upload, 0, len(localRefs))
			for _, ref := range localRefs {
				uploads = append(uploads, buildapiclient.Upload{SourcePath: ref["source_path"], DestPath: ref["source_path"]})
			}

			uploadDeadline := time.Now().Add(10 * time.Minute)
			for {
				if err := api.UploadFiles(ctx, resp.Name, uploads); err != nil {
					lower := strings.ToLower(err.Error())
					if time.Now().After(uploadDeadline) {
						handleError(fmt.Errorf("upload files failed: %w", err))
					}
					if strings.Contains(lower, "503") || strings.Contains(lower, "service unavailable") || strings.Contains(lower, "upload pod not ready") {
						fmt.Println("Upload server not ready yet. Retrying...")
						time.Sleep(5 * time.Second)
						continue
					}
					handleError(fmt.Errorf("upload files failed: %w", err))
				}
				break
			}
			fmt.Println("Local files uploaded. Build will proceed.")
		}

		if waitForBuild || followLogs || download {
			fmt.Println("Waiting for build to complete...")
			timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Minute)
			defer cancel()
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			userFollowRequested := followLogs
			var lastPhase, lastMessage string
			logFollowWarned := false

			logClient := &http.Client{
				Timeout: 10 * time.Minute,
				Transport: &http.Transport{
					ResponseHeaderTimeout: 30 * time.Second,
					IdleConnTimeout:       2 * time.Minute,
				},
			}

			for {
				select {
				case <-timeoutCtx.Done():
					handleError(fmt.Errorf("timed out waiting for build"))
				case <-ticker.C:
					if followLogs {
						req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(serverURL, "/")+"/v1/builds/"+url.PathEscape(resp.Name)+"/logs?follow=1", nil)
						if strings.TrimSpace(authToken) != "" {
							req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(authToken))
						}
						resp2, err := logClient.Do(req)
						if err == nil && resp2.StatusCode == http.StatusOK {
							fmt.Println("Streaming logs...")
							io.Copy(os.Stdout, resp2.Body)
							resp2.Body.Close()
							followLogs = userFollowRequested
						} else if resp2 != nil {
							body, _ := io.ReadAll(resp2.Body)
							msg := strings.TrimSpace(string(body))
							if resp2.StatusCode == http.StatusServiceUnavailable || resp2.StatusCode == http.StatusGatewayTimeout {
								if !logFollowWarned {
									fmt.Println("log stream not ready (HTTP", resp2.StatusCode, "). Retrying…")
									logFollowWarned = true
								}
								// treat as transient; keep trying silently afterwards
							} else {
								if msg != "" {
									fmt.Printf("log stream error (%d): %s\n", resp2.StatusCode, msg)
								} else {
									fmt.Printf("log stream error: HTTP %d\n", resp2.StatusCode)
								}
								followLogs = false
							}
							resp2.Body.Close()
						}
					}
					reqCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
					st, err := api.GetBuild(reqCtx, resp.Name)
					cancel()
					if err != nil {
						fmt.Printf("status check failed: %v\n", err)
						continue
					}
					if !userFollowRequested {
						if st.Phase != lastPhase || st.Message != lastMessage {
							fmt.Printf("status: %s - %s\n", st.Phase, st.Message)
							lastPhase = st.Phase
							lastMessage = st.Message
						}
					}
					if st.Phase == "Completed" {
						if download {
							if err := downloadArtifactViaAPI(ctx, serverURL, resp.Name, outputDir); err != nil {
								fmt.Printf("Download via API failed: %v\n", err)
							}
							return
						}
						return
					}
					if st.Phase == "Failed" {
						handleError(fmt.Errorf("build failed: %s", st.Message))
					}
				}
			}
		}
		return
	}

}

func validateBuildRequirements() error {
	if manifest == "" {
		return fmt.Errorf("--manifest is required")
	}

	if buildName == "" {
		return fmt.Errorf("name is required")
	}

	if strings.TrimSpace(architecture) == "" {
		return fmt.Errorf("--arch is required")
	}

	return nil
}

func handleError(err error) {
	fmt.Printf("Error: %v\n", err)
	os.Exit(1)
}

func findLocalFileReferences(manifestContent string) ([]map[string]string, error) {
	var manifestData map[string]any
	var localFiles []map[string]string

	if err := yaml.Unmarshal([]byte(manifestContent), &manifestData); err != nil {
		return nil, fmt.Errorf("failed to parse manifest YAML: %w", err)
	}

	isPathSafe := func(path string) error {
		if path == "" || path == "/" {
			return fmt.Errorf("empty or root path is not allowed")
		}

		if strings.Contains(path, "..") {
			return fmt.Errorf("directory traversal detected in path: %s", path)
		}

		if filepath.IsAbs(path) {
			// TODO add safe dirs flag
			safeDirectories := []string{}
			isInSafeDir := false
			for _, dir := range safeDirectories {
				if strings.HasPrefix(path, dir+"/") {
					isInSafeDir = true
					break
				}
			}
			if !isInSafeDir {
				return fmt.Errorf("absolute path outside safe directories: %s", path)
			}
		}

		return nil
	}

	processAddFiles := func(addFiles []any) error {
		for _, file := range addFiles {
			if fileMap, ok := file.(map[string]any); ok {
				path, hasPath := fileMap["path"].(string)
				sourcePath, hasSourcePath := fileMap["source_path"].(string)
				if hasPath && hasSourcePath {
					if err := isPathSafe(sourcePath); err != nil {
						return err
					}
					localFiles = append(localFiles, map[string]string{
						"path":        path,
						"source_path": sourcePath,
					})
				}
			}
		}
		return nil
	}

	if content, ok := manifestData["content"].(map[string]any); ok {
		if addFiles, ok := content["add_files"].([]any); ok {
			if err := processAddFiles(addFiles); err != nil {
				return nil, err
			}
		}
	}

	if qm, ok := manifestData["qm"].(map[string]any); ok {
		if qmContent, ok := qm["content"].(map[string]any); ok {
			if addFiles, ok := qmContent["add_files"].([]any); ok {
				if err := processAddFiles(addFiles); err != nil {
					return nil, err
				}
			}
		}
	}

	return localFiles, nil
}

func downloadArtifactViaAPI(ctx context.Context, baseURL, name, outDir string) error {
	if strings.TrimSpace(outDir) == "" {
		outDir = "./output"
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	base := strings.TrimRight(baseURL, "/")
	urlStr := base + "/v1/builds/" + url.PathEscape(name) + "/artifact"

	deadline := time.Now().Add(30 * time.Minute)

	httpClient := &http.Client{
		Timeout: 30 * time.Minute,
		Transport: &http.Transport{
			ResponseHeaderTimeout: 2 * time.Minute,
			IdleConnTimeout:       5 * time.Minute,
		},
	}

	warned := false
	for {
		if ctx.Err() != nil || time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for artifact to become ready")
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
		if strings.TrimSpace(authToken) != "" {
			req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(authToken))
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			time.Sleep(3 * time.Second)
			continue
		}

		if resp.StatusCode == http.StatusOK {
			filename := name + ".artifact"
			contentType := resp.Header.Get("Content-Type")
			if cd := resp.Header.Get("Content-Disposition"); cd != "" {
				if i := strings.Index(cd, "filename="); i >= 0 {
					f := strings.Trim(cd[i+9:], "\" ")
					if f != "" {
						filename = f
					}
				}
			}
			if at := strings.TrimSpace(resp.Header.Get("X-AIB-Artifact-Type")); at != "" {
				fmt.Printf("Artifact type: %s\n", at)
			}
			if comp := strings.TrimSpace(resp.Header.Get("X-AIB-Compression")); comp != "" {
				fmt.Printf("Compression: %s\n", comp)
			}
			if root := strings.TrimSpace(resp.Header.Get("X-AIB-Archive-Root")); root != "" {
				fmt.Printf("Archive root: %s\n", root)
			}
			outPath := filepath.Join(outDir, filename)
			tmp := outPath + ".partial"
			f, err := os.Create(tmp)
			if err != nil {
				resp.Body.Close()
				return err
			}
			if cl := strings.TrimSpace(resp.Header.Get("Content-Length")); cl != "" {
				// Known size: nice progress bar
				// Convert to int64
				var total int64
				fmt.Sscan(cl, &total)
				bar := progressbar.NewOptions64(
					total,
					progressbar.OptionSetDescription("Downloading"),
					progressbar.OptionShowBytes(true),
					progressbar.OptionSetWidth(15),
					progressbar.OptionThrottle(65*time.Millisecond),
					progressbar.OptionShowCount(),
					progressbar.OptionClearOnFinish(),
				)
				reader := io.TeeReader(resp.Body, bar)
				if _, copyErr := io.Copy(f, reader); copyErr != nil {
					f.Close()
					os.Remove(tmp)
					return copyErr
				}
				_ = bar.Finish()
				fmt.Println()
			} else {
				bar := progressbar.NewOptions(
					-1,
					progressbar.OptionSetDescription("Downloading"),
					progressbar.OptionSpinnerType(14),
					progressbar.OptionClearOnFinish(),
				)
				reader := io.TeeReader(resp.Body, bar)
				if _, copyErr := io.Copy(f, reader); copyErr != nil {
					f.Close()
					os.Remove(tmp)
					return copyErr
				}
				_ = bar.Finish()
				fmt.Println()
			}
			resp.Body.Close()
			f.Close()
			if err := os.Rename(tmp, outPath); err != nil {
				return err
			}
			fmt.Printf("Artifact downloaded to %s\n", outPath)

			// If the artifact is a tar archive (directory export), optionally extract it
			if strings.HasPrefix(contentType, "application/x-tar") || strings.HasPrefix(contentType, "application/gzip") || strings.HasSuffix(strings.ToLower(outPath), ".tar") || strings.HasSuffix(strings.ToLower(outPath), ".tar.gz") {
				if !compressArtifacts {
					destDir := strings.TrimSuffix(outPath, ".tar")
					destDir = strings.TrimSuffix(destDir, ".gz")
					if err := os.MkdirAll(destDir, 0o755); err != nil {
						return fmt.Errorf("create extract dir: %w", err)
					}
					if err := extractTar(outPath, destDir); err != nil {
						return fmt.Errorf("extract tar: %w", err)
					}
					fmt.Printf("Extracted to %s\n", destDir)
				}
			}
			return nil
		}

		body, _ := io.ReadAll(resp.Body)
		msg := strings.ToLower(strings.TrimSpace(string(body)))
		resp.Body.Close()
		if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusConflict || strings.Contains(msg, "not ready") {
			if !warned {
				fmt.Println("Artifact not ready yet. Waiting...")
				warned = true
			}
			time.Sleep(3 * time.Second)
			continue
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

func extractTar(tarPath, destDir string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(strings.ToLower(tarPath), ".gz") {
		gr, gzErr := gzip.NewReader(f)
		if gzErr == nil {
			defer gr.Close()
			r = gr
		}
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		targetPath := filepath.Join(destDir, hdr.Name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, targetPath); err != nil && !os.IsExist(err) {
				return err
			}
		default:
			// ignore other types
		}
	}
	return nil
}

func runDownload(cmd *cobra.Command, args []string) {
	ctx := context.Background()

	if strings.TrimSpace(serverURL) == "" {
		fmt.Println("Error: --server is required (or set CAIB_SERVER)")
		os.Exit(1)
	}

	if strings.TrimSpace(authToken) == "" {
		if tok, err := loadTokenFromKubeconfig(); err == nil && strings.TrimSpace(tok) != "" {
			authToken = tok
		}
	}
	var opts []buildapiclient.Option
	if strings.TrimSpace(authToken) != "" {
		opts = append(opts, buildapiclient.WithAuthToken(strings.TrimSpace(authToken)))
	}
	api, err := buildapiclient.New(serverURL, opts...)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	st, err := api.GetBuild(ctx, buildName)
	if err != nil {
		fmt.Printf("Error getting build %s: %v\n", buildName, err)
		os.Exit(1)
	}
	if st.Phase != "Completed" {
		fmt.Printf("Build %s is not completed (status: %s). Cannot download artifacts.\n", buildName, st.Phase)
		os.Exit(1)
	}

	if err := downloadArtifactViaAPI(ctx, serverURL, buildName, outputDir); err != nil {
		fmt.Printf("Download failed: %v\n", err)
		os.Exit(1)
	}
}

func runList(cmd *cobra.Command, args []string) {
	ctx := context.Background()
	if strings.TrimSpace(serverURL) == "" {
		fmt.Println("Error: --server is required (or set CAIB_SERVER)")
		os.Exit(1)
	}
	if strings.TrimSpace(authToken) == "" {
		if tok, err := loadTokenFromKubeconfig(); err == nil && strings.TrimSpace(tok) != "" {
			authToken = tok
		}
	}
	var opts []buildapiclient.Option
	if strings.TrimSpace(authToken) != "" {
		opts = append(opts, buildapiclient.WithAuthToken(strings.TrimSpace(authToken)))
	}
	api, err := buildapiclient.New(serverURL, opts...)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	items, err := api.ListBuilds(ctx)
	if err != nil {
		fmt.Printf("Error listing ImageBuilds: %v\n", err)
		os.Exit(1)
	}
	if len(items) == 0 {
		fmt.Println("No ImageBuilds found")
		return
	}
	fmt.Printf("%-20s %-12s %-20s %-20s %-20s\n", "NAME", "STATUS", "MESSAGE", "CREATED", "ARTIFACT")
	for _, it := range items {
		fmt.Printf("%-20s %-12s %-20s %-20s %-20s\n", it.Name, it.Phase, it.Message, it.CreatedAt, "")
	}
}

func loadTokenFromKubeconfig() (string, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	// First, ask client-go to build a client config. This will execute any exec credential plugins
	// (e.g., OpenShift login) and populate a usable BearerToken.
	deferred := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})
	if restCfg, err := deferred.ClientConfig(); err == nil && restCfg != nil {
		if t := strings.TrimSpace(restCfg.BearerToken); t != "" {
			return t, nil
		}
		if f := strings.TrimSpace(restCfg.BearerTokenFile); f != "" {
			if b, rerr := os.ReadFile(f); rerr == nil {
				if t := strings.TrimSpace(string(b)); t != "" {
					return t, nil
				}
			}
		}
	}

	// Fallback to parsing raw kubeconfig for legacy token fields
	rawCfg, err := loadingRules.Load()
	if err != nil || rawCfg == nil {
		return "", fmt.Errorf("cannot load kubeconfig: %w", err)
	}
	ctxName := rawCfg.CurrentContext
	if strings.TrimSpace(ctxName) == "" {
		return "", fmt.Errorf("no current kube context")
	}
	ctx := rawCfg.Contexts[ctxName]
	if ctx == nil {
		return "", fmt.Errorf("missing context %s", ctxName)
	}
	ai := rawCfg.AuthInfos[ctx.AuthInfo]
	if ai == nil {
		return "", fmt.Errorf("missing auth info for context %s", ctxName)
	}
	if strings.TrimSpace(ai.Token) != "" {
		return strings.TrimSpace(ai.Token), nil
	}
	if ai.AuthProvider != nil && ai.AuthProvider.Config != nil {
		if t := strings.TrimSpace(ai.AuthProvider.Config["access-token"]); t != "" {
			return t, nil
		}
		if t := strings.TrimSpace(ai.AuthProvider.Config["id-token"]); t != "" {
			return t, nil
		}
		if t := strings.TrimSpace(ai.AuthProvider.Config["token"]); t != "" {
			return t, nil
		}
	}
	if path, err := exec.LookPath("oc"); err == nil && path != "" {
		out, err := exec.Command(path, "whoami", "-t").Output()
		if err == nil {
			if t := strings.TrimSpace(string(out)); t != "" {
				return t, nil
			}
		}
	}
	return "", fmt.Errorf("no bearer token found in kubeconfig")
}
