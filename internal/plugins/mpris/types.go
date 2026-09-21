package mpris

import "time"

type MPRISRequest struct {
	// Request fields
	RequestPlayerList bool `json:"requestPlayerList,omitempty"`
	RequestNowPlaying bool `json:"requestNowPlaying,omitempty"`
	RequestVolume     bool `json:"requestVolume,omitempty"`

	// Action fields
	Player        string `json:"player,omitempty"`
	Action        string `json:"action,omitempty"`
	SetVolume     *int   `json:"setVolume,omitempty"`
	Seek          *int64 `json:"Seek,omitempty"`
	SetPosition   *int64 `json:"SetPosition,omitempty"`
	SetShuffle    *bool  `json:"setShuffle,omitempty"`
	SetLoopStatus string `json:"setLoopStatus,omitempty"`

	// Album art
	AlbumArtUrl string `json:"albumArtUrl,omitempty"`

	// Set when this packet carries album art bytes over a side channel.
	TransferringAlbumArt bool `json:"transferringAlbumArt,omitempty"`

	// Player list from remote device
	PlayerList []string `json:"playerList,omitempty"`

	// NowPlaying fields — populated when phone sends state update
	Title          string `json:"title,omitempty"`
	Artist         string `json:"artist,omitempty"`
	Album          string `json:"album,omitempty"`
	Url            string `json:"url,omitempty"`
	Length         int64  `json:"length,omitempty"`
	Pos            int64  `json:"pos,omitempty"`
	IsPlaying      bool   `json:"isPlaying,omitempty"`
	Volume         int    `json:"volume,omitempty"`
	CanControl     bool   `json:"canControl,omitempty"`
	CanGoNext      bool   `json:"canGoNext,omitempty"`
	CanGoPrevious  bool   `json:"canGoPrevious,omitempty"`
	CanPause       bool   `json:"canPause,omitempty"`
	CanPlay        bool   `json:"canPlay,omitempty"`
	CanSeek        bool   `json:"canSeek,omitempty"`
	PlaybackStatus string `json:"playbackStatus,omitempty"`
	Shuffle        *bool  `json:"shuffle,omitempty"`
	LoopStatus     string `json:"loopStatus,omitempty"`
}

type NowPlaying struct {
	Player      string `json:"player"`
	Title       string `json:"title"`
	Artist      string `json:"artist"`
	Album       string `json:"album"`
	AlbumArtUrl string `json:"albumArtUrl"`
	ArtPending  bool   `json:"artPending,omitempty"`
	Url         string `json:"url,omitempty"`
	Length      int64  `json:"length"`
	Pos         int64  `json:"pos,omitempty"`
	// PosAnchorMs is the wall-clock time (Unix millis) at which Pos was
	// sampled. Clients compute the live position drift-free as
	// Pos + (nowMs - PosAnchorMs) * (isPlaying ? 1 : 0).
	PosAnchorMs    int64  `json:"posAnchorMs,omitempty"`
	IsPlaying      bool   `json:"isPlaying"`
	Volume         int    `json:"volume,omitempty"`
	CanControl     bool   `json:"canControl"`
	CanGoNext      bool   `json:"canGoNext"`
	CanGoPrevious  bool   `json:"canGoPrevious"`
	CanPause       bool   `json:"canPause"`
	CanPlay        bool   `json:"canPlay"`
	CanSeek        bool   `json:"canSeek"`
	PlaybackStatus string `json:"playbackStatus"`
	Shuffle        *bool  `json:"shuffle,omitempty"`
	LoopStatus     string `json:"loopStatus,omitempty"`
}

// DeepCopy returns a fully independent copy of NowPlaying.
// Pointer fields (Shuffle) are deep-copied to prevent shared-memory races
// between the cached state and callers.
func (p *NowPlaying) DeepCopy() *NowPlaying {
	if p == nil {
		return nil
	}
	cp := *p
	if p.Shuffle != nil {
		s := *p.Shuffle
		cp.Shuffle = &s
	}
	return &cp
}

type DebugPlayerInfo struct {
	DisplayName    string `json:"displayName"`
	BusName        string `json:"busName"`
	ShortName      string `json:"shortName"`
	Title          string `json:"title"`
	Artist         string `json:"artist"`
	Album          string `json:"album"`
	PlaybackStatus string `json:"playbackStatus"`
	IsPlaying      bool   `json:"isPlaying"`
	Volume         int    `json:"volume,omitempty"`
	Pos            int64  `json:"pos"`
	// PosAnchorMs mirrors NowPlaying.PosAnchorMs: the wall-clock time the
	// cached Pos was last broadcast, for client-side extrapolation.
	PosAnchorMs   int64  `json:"posAnchorMs,omitempty"`
	Length        int64  `json:"length"`
	AlbumArtUrl   string `json:"albumArtUrl"`
	CanSeek       bool   `json:"canSeek"`
	CanGoNext     bool   `json:"canGoNext"`
	CanGoPrevious bool   `json:"canGoPrevious"`
	CanPlay       bool   `json:"canPlay"`
	CanPause      bool   `json:"canPause"`
	Error         string `json:"error,omitempty"`
}

type DebugStatus struct {
	WatcherRunning bool              `json:"watcherRunning"`
	DeviceCount    int               `json:"deviceCount"`
	Players        []DebugPlayerInfo `json:"players"`
	PlayerMappings map[string]string `json:"playerMappings"`
}

// remoteStatePollInterval is how often the daemon re-requests now-playing
// from devices with an active remote player. Clients are then pure-push:
// fresh state arrives within one interval of connect, and position stays
// current without any client-side polling.
const remoteStatePollInterval = 5 * time.Second
