package sftp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
)

// SftpPlugin handles KDE Connect SFTP negotiation and optional sshfs mounting.
type SftpPlugin struct {
	cfg         config.SFTPConfig
	bus         *events.Bus
	logger      log.Logger
	mu          sync.RWMutex
	lastBody    map[string]SftpBody
	pending     map[string]chan sftpResult
	mountPoints map[string]string // deviceID -> local mountPoint path
	// mountReadOnly records the mode each mount was created with, so a later
	// --ro request against an existing mount can report what it actually is.
	mountReadOnly map[string]bool
	// warnedDirs records mount directories the document-folder warning has
	// already fired for, so it is logged once per location per run.
	warnedDirs map[string]bool
	mountPIDs  map[string]int // deviceID -> sshfs PID for graceful shutdown
}

func NewSftpPlugin(cfg config.SFTPConfig, bus *events.Bus, logger log.Logger) *SftpPlugin {
	return &SftpPlugin{
		cfg:           cfg,
		bus:           bus,
		logger:        logger.With(log.String("plugin", "sftp")),
		lastBody:      make(map[string]SftpBody),
		pending:       make(map[string]chan sftpResult),
		mountPoints:   make(map[string]string),
		mountReadOnly: make(map[string]bool),
		mountPIDs:     make(map[string]int),
	}
}

// SftpBody matches the body of a kdeconnect.sftp packet sent by the Android app.
type SftpBody struct {
	ServerRunning *bool       `json:"serverRunning,omitempty"`
	IP            string      `json:"ip"`
	Port          json.Number `json:"port"`
	User          string      `json:"user"`
	// Password is intentionally not logged.
	Password string `json:"password"`
	// Path is the primary storage root (the volume path, or "/" with several
	// volumes for legacy); prefer MultiPaths as the authoritative list.
	Path string `json:"path"`
	// MultiPaths lists all available storage root paths on the device
	// (e.g. internal storage, SD card). Populated by Android API 30+.
	MultiPaths []string `json:"multiPaths,omitempty"`
	// PathNames provides human-readable labels for each path in MultiPaths.
	PathNames []string `json:"pathNames,omitempty"`
	// ErrorMessage is set when the device cannot start the SFTP server
	// (e.g. missing storage permissions).
	ErrorMessage string `json:"errorMessage,omitempty"`
}

type sftpResult struct {
	body SftpBody
	err  error
}

// StorageVolume describes a single browsable storage root on the device.
type StorageVolume struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// SftpInfo holds cached SFTP details; Password stays empty unless asked for,
// omitempty keeping it out of the JSON so masked never reads as passwordless.
type SftpInfo struct {
	IP       string          `json:"ip"`
	Port     json.Number     `json:"port"`
	User     string          `json:"user"`
	Password string          `json:"password,omitempty"`
	Path     string          `json:"path"`
	Volumes  []StorageVolume `json:"volumes,omitempty"`

	// Mounted/MountPoint let clients render a toggle without inspecting the
	// host's mount table.
	Mounted    bool   `json:"mounted"`
	MountPoint string `json:"mountPoint,omitempty"`
}

func (p *SftpPlugin) Name() string            { return "SFTP" }
func (p *SftpPlugin) Timeout() time.Duration  { return 5 * time.Second }
func (p *SftpPlugin) IncomingTypes() []string { return []string{"kdeconnect.sftp"} }
func (p *SftpPlugin) OutgoingTypes() []string { return []string{"kdeconnect.sftp.request"} }

// mountPointFor derives the mount path per device (identifiable after a
// restart); empty MountDir means the default, never a tmp dir.
func (p *SftpPlugin) mountPointFor(deviceID string) string {
	baseDir := p.cfg.MountDir
	if baseDir == "" {
		baseDir = config.DefaultMountDir()
	}
	return confinedMountPath(baseDir, "kcd-sftp-"+deviceID)
}

// legacyMountPointFor is where mounts lived before the default moved; they
// outlive the daemon, so Unmount must still find and release them.
func legacyMountPointFor(deviceID string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return confinedMountPath(filepath.Join(home, "Downloads", "kcd", "mnt"), "kcd-sftp-"+deviceID)
}

func confinedMountPath(baseDir, child string) string {
	base, err := filepath.Abs(baseDir)
	if err != nil {
		return ""
	}
	target := filepath.Join(base, child)
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return ""
	}
	return target
}
