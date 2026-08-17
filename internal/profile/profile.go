// Package profile holds the user configuration: where the WireGuard configs
// live, and which tunnel belongs to which group depending on the network.
//
// Everything here comes from the user's file: the binary itself knows no
// tunnel name and no network.
package profile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"ledez.net/tun-manager/internal/netctx"
	"ledez.net/tun-manager/internal/wg"
)

// Defaults for the environment only. Group membership and network contexts are
// deliberately empty: they describe one person's tunnels, so they belong in the
// user's configuration file, not in the binary.
const (
	DefaultConfigDir = "/private/wireguard/config"
	DefaultWgQuick   = "/opt/homebrew/bin/wg-quick"
	DefaultRefresh   = 5 * time.Minute

	// DefaultFeedSocket is where the status feed binds. /var/run is cleared on
	// reboot, which disposes of a socket left behind by a crash for free.
	DefaultFeedSocket = "/var/run/tun-manager.sock"

	// GroupAll is the reserved group holding every tunnel to shut down. It is
	// never affected by overrides and never reported as a tunnel's own group.
	GroupAll = "all"
	// GroupNeeded is the group started automatically.
	GroupNeeded = "needed"
	// GroupExtra is the group offered for selection.
	GroupExtra = "extra"
)

// Override moves a tunnel between groups depending on the detected network.
type Override struct {
	Tunnel string `yaml:"tunnel"`
	// GroupWhen maps a context name to a group. The "default" key applies when
	// no context rule matched.
	GroupWhen map[string]string `yaml:"group_when"`
}

// Config is the whole user configuration.
type Config struct {
	ConfigDir string `yaml:"config_dir"`
	WgQuick   string `yaml:"wg_quick"`
	// RunDir is where wg-quick records the interface name of each live tunnel.
	RunDir          string        `yaml:"run_dir"`
	RefreshInterval time.Duration `yaml:"refresh_interval"`
	Notify          bool          `yaml:"notify"`
	// Feed publishes state on a unix socket for a menu bar application to
	// read. Nothing on that socket can start or stop a tunnel.
	Feed bool `yaml:"feed"`
	// FeedSocket is where that socket is bound.
	FeedSocket string              `yaml:"feed_socket"`
	Contexts   []netctx.Rule       `yaml:"contexts"`
	Groups     map[string][]string `yaml:"groups"`
	Overrides  []Override          `yaml:"overrides"`

	// Path is the file the configuration was read from, for `doctor`.
	Path string `yaml:"-"`
	// IsDefault reports that no configuration file was found.
	IsDefault bool `yaml:"-"`
}

// Default returns the built-in configuration: paths and intervals, and nothing
// else. Without a configuration file every tunnel is listed and none is in a
// group, so the group commands and keys have nothing to act on.
func Default() *Config {
	return &Config{
		ConfigDir:       DefaultConfigDir,
		WgQuick:         DefaultWgQuick,
		RunDir:          wg.DefaultRunDir,
		RefreshInterval: DefaultRefresh,
		Notify:          true,
		Feed:            true,
		FeedSocket:      DefaultFeedSocket,
		Groups:          map[string][]string{},
	}
}

// HasGroups reports whether any group has a member. When it does not, the
// group commands cannot do anything, which is worth telling the user about.
func (c *Config) HasGroups() bool {
	for _, members := range c.Groups {
		if len(members) > 0 {
			return true
		}
	}
	return len(c.Overrides) > 0
}

// Load reads a configuration file. A missing file is not an error: the built-in
// defaults are returned instead.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		cfg := Default()
		cfg.Path = path
		cfg.IsDefault = true
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.Path = path
	cfg.applyDefaults()
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	d := Default()
	if c.ConfigDir == "" {
		c.ConfigDir = d.ConfigDir
	}
	if c.WgQuick == "" {
		c.WgQuick = d.WgQuick
	}
	if c.RunDir == "" {
		c.RunDir = d.RunDir
	}
	if c.FeedSocket == "" {
		c.FeedSocket = d.FeedSocket
	}
	if c.RefreshInterval <= 0 {
		c.RefreshInterval = d.RefreshInterval
	}
	if c.Groups == nil {
		c.Groups = map[string][]string{}
	}
}

// GroupOf returns the group a tunnel belongs to in the given network context,
// or an empty string when it belongs to none. The reserved "all" group is never
// returned.
func (c *Config) GroupOf(tunnel, context string) string {
	for _, o := range c.Overrides {
		if o.Tunnel != tunnel {
			continue
		}
		if group, ok := o.GroupWhen[context]; ok {
			return group
		}
		return o.GroupWhen[netctx.Default]
	}

	for group, members := range c.Groups {
		if group == GroupAll {
			continue
		}
		for _, m := range members {
			if m == tunnel {
				return group
			}
		}
	}
	return ""
}

// Members lists the tunnels of a group for the given context, preserving the
// declaration order and appending the tunnels moved in by an override.
func (c *Config) Members(group, context string) []string {
	if group == GroupAll {
		return append([]string(nil), c.Groups[GroupAll]...)
	}

	overridden := make(map[string]bool, len(c.Overrides))
	for _, o := range c.Overrides {
		overridden[o.Tunnel] = true
	}

	out := make([]string, 0, len(c.Groups[group]))
	for _, name := range c.Groups[group] {
		if !overridden[name] {
			out = append(out, name)
		}
	}
	for _, o := range c.Overrides {
		if c.GroupOf(o.Tunnel, context) == group {
			out = append(out, o.Tunnel)
		}
	}
	return out
}
