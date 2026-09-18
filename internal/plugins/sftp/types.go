package sftp

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/events"
	"go.uber.org/zap"
)

// SftpPlugin handles KDE Connect SFTP negotiation and optional sshfs mounting.
type SftpPlugin struct {
	cfg         config.SFTPConfig
	bus         *events.Bus
	logger      *zap.Logger
	mu          sync.RWMutex
	lastBody    map[string]SftpBody
	mountPoints map[string]string // deviceID -> local mountPoint path
	mountPIDs   map[string]int    // deviceID -> sshfs PID for graceful shutdown
}

func NewSftpPlugin(cfg config.SFTPConfig, bus *events.Bus, logger *zap.Logger) *SftpPlugin {
	return &SftpPlugin{
		cfg:         cfg,
		bus:         bus,
		logger:      logger.With(zap.String("plugin", "sftp")),
		lastBody:    make(map[string]SftpBody),
		mountPoints: make(map[string]string),
		mountPIDs:   make(map[string]int),
	}
}

// SftpBody matches the body of a kdeconnect.sftp packet sent by the Android app.
type SftpBody struct {
	IP   string      `json:"ip"`
	Port json.Number `json:"port"`
	User string      `json:"user"`
	// Password is intentionally not logged.
	Password string `json:"password"`
	// Path is the primary storage root path from the Android device.
	// When exactly one volume exists this is the volume path (e.g. /storage/emulated/0);
	// when multiple volumes exist this falls back to "/" (legacy compat).
	// Prefer MultiPaths for the authoritative list of browsable roots.
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

// StorageVolume describes a single browsable storage root on the device.
type StorageVolume struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// SftpInfo holds the complete cached SFTP connection details for a device.
type SftpInfo struct {
	IP       string          `json:"ip"`
	Port     json.Number     `json:"port"`
	User     string          `json:"user"`
	Password string          `json:"password"`
	Path     string          `json:"path"`
	Volumes  []StorageVolume `json:"volumes,omitempty"`
}

func (p *SftpPlugin) Name() string            { return "SFTP" }
func (p *SftpPlugin) Timeout() time.Duration  { return 5 * time.Second }
func (p *SftpPlugin) IncomingTypes() []string { return []string{"kdeconnect.sftp"} }
func (p *SftpPlugin) OutgoingTypes() []string { return []string{"kdeconnect.sftp.request"} }
