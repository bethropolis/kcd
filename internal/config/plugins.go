package config

import (
	"os"
	"path/filepath"
)

type PluginConfig struct {
	Battery           bool `toml:"battery"`
	Clipboard         bool `toml:"clipboard"`
	Notification      bool `toml:"notification"`
	Share             bool `toml:"share"`
	RunCommand        bool `toml:"runcommand"`
	MPRIS             bool `toml:"mpris"`
	Ping              bool `toml:"ping"`
	Telephony         bool `toml:"telephony"`
	Connectivity      bool `toml:"connectivity"`
	Mousepad          bool `toml:"mousepad"`
	SFTP              bool `toml:"sftp"`
	FindMyPhone       bool `toml:"findmyphone"`
	LockDevice        bool `toml:"lockdevice"`
	SystemVolume      bool `toml:"systemvolume"`
	PauseMusic        bool `toml:"pausemusic"`
	SendNotifications bool `toml:"sendnotifications"`
	SMS               bool `toml:"sms"`
	Presenter         bool `toml:"presenter"`
}

type BatteryConfig struct {
	NotifyLow   bool   `toml:"notify_low"`
	NotifyFull  bool   `toml:"notify_full"`
	LowUrgency  string `toml:"low_urgency"`
	FullUrgency string `toml:"full_urgency"`
	LowMessage  string `toml:"low_message"`
	FullMessage string `toml:"full_message"`
}

type NotificationPluginConfig struct {
	Urgency       string `toml:"urgency"`
	FetchIcons    bool   `toml:"fetch_icons"`
	IconCacheDir  string `toml:"icon_cache_dir"`
	MaxBodyLength int    `toml:"max_body_length"`
	ExpireMS      int    `toml:"expire_ms"`
}

type ShareConfig struct {
	PortMin           int    `toml:"port_min"`
	PortMax           int    `toml:"port_max"`
	AcceptTimeoutSecs int    `toml:"accept_timeout_secs"`
	AutoOpen          bool   `toml:"auto_open"`
	OpenCommand       string `toml:"open_command"`
	Overwrite         bool   `toml:"overwrite"`
}

type SFTPConfig struct {
	MountDir               string   `toml:"mount_dir"`
	CredentialsTimeoutSecs int      `toml:"credentials_timeout_secs"`
	KeepaliveIntervalSecs  int      `toml:"keepalive_interval_secs"`
	KeepaliveCount         int      `toml:"keepalive_count"`
	AutoOpen               bool     `toml:"auto_open"`
	OpenCommand            string   `toml:"open_command"`
	ExtraSshfsOpts         []string `toml:"extra_sshfs_opts"`
}

type PingConfig struct {
	AppName        string `toml:"app_name"`
	Icon           string `toml:"icon"`
	DefaultMessage string `toml:"default_message"`
}

type PairingConfig struct {
	TimeoutSecs int `toml:"timeout_secs"`
}

type MousepadConfig struct {
	Backend string `toml:"backend"`
}

func (p *PluginConfig) Defaults() {
	p.Battery = true
	p.Clipboard = true
	p.Notification = true
	p.Share = true
	p.RunCommand = true
	p.MPRIS = true
	p.Ping = true
	p.Telephony = true
	p.Connectivity = true
	p.Mousepad = true
	p.SFTP = true
	p.FindMyPhone = true
	p.LockDevice = true
	p.SystemVolume = true
	p.PauseMusic = true
	p.SendNotifications = true
	p.SMS = true
	p.Presenter = true
}

func (c *BatteryConfig) Defaults() {
	c.NotifyLow = true
	c.NotifyFull = true
	c.LowUrgency = "critical"
	c.FullUrgency = "normal"
	c.LowMessage = "Battery low"
	c.FullMessage = "Battery fully charged"
}

func (c *NotificationPluginConfig) Defaults() {
	c.FetchIcons = true
	c.MaxBodyLength = 0
	c.ExpireMS = -1
}

func (c *ShareConfig) Defaults() {
	c.PortMin = 1739
	c.PortMax = 1764
	c.AcceptTimeoutSecs = 120
	c.OpenCommand = "xdg-open"
}

func (c *SFTPConfig) Defaults() {
	home, _ := os.UserHomeDir()
	c.MountDir = filepath.Join(home, "Downloads", "kcd", "mnt")
	c.CredentialsTimeoutSecs = 20
	c.KeepaliveIntervalSecs = 15
	c.KeepaliveCount = 3
	c.AutoOpen = true
	c.OpenCommand = "xdg-open"
}

func (c *PingConfig) Defaults() {
	c.AppName = "KDE Connect"
	c.Icon = "smartphone"
}

func (c *PairingConfig) Defaults() {
	c.TimeoutSecs = 30
}

func (c *MousepadConfig) Defaults() {
	c.Backend = "auto"
}
