// Package profile holds the user configuration: where the WireGuard configs
// live, and which tunnel belongs to which group depending on the network.
//
// Everything here comes from the user's file: the binary itself knows no
// tunnel name and no network.
package profile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"ledez.net/tun-manager/internal/netctx"
)

// Defaults for the environment only. Group membership and network contexts are
// deliberately empty: they describe one person's tunnels, so they belong in the
// user's configuration file, not in the binary.
const (
	DefaultWgQuick = "/opt/homebrew/bin/wg-quick"
	DefaultRefresh = 5 * time.Minute

	// MinRefresh is the fastest this program will refresh on its own.
	//
	// A refresh reads the WireGuard control sockets, which is work root does
	// on behalf of a file a plain user writes: `refresh_interval: 1ns` is
	// positive, so it passed the "unset falls back to the default" rule, and
	// left a root process reading those sockets as fast as it could. One second
	// is far below anything a person watching a table would notice and far
	// above a loop.
	//
	// A value under it is raised rather than refused. A configuration that will
	// not load is a program that will not start, which is a much worse way to
	// lose than a table that refreshes a little slower than asked.
	MinRefresh = time.Second

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
//
// What is missing from it is the point: nothing here decides which binary root
// executes or which directory it reads as the list of tunnels. Those live in
// Privileged, in a file only root can write.
type Config struct {
	// ConfigDir is where the .conf files are read from. It is not a setting:
	// it is always profile.ConfigDir, there is no key for it and no flag under
	// root. The field exists because the tests, and the simulator, have to
	// point at a directory they are allowed to write — and a package that can
	// only be tested as root is a package nobody tests.
	ConfigDir       string              `yaml:"-"`
	RefreshInterval time.Duration       `yaml:"refresh_interval"`
	Contexts        []netctx.Rule       `yaml:"contexts"`
	Groups          map[string][]string `yaml:"groups"`
	Overrides       []Override          `yaml:"overrides"`

	// RefreshRaisedFrom is what the file asked for when that was below
	// MinRefresh, and zero otherwise. Kept so `doctor` can say the setting was
	// not applied as written: a value silently changed is the thing this
	// program refuses to do elsewhere.
	RefreshRaisedFrom time.Duration `yaml:"-"`

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
		ConfigDir:       ConfigDir,
		RefreshInterval: DefaultRefresh,
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

	// Unmarshalled *into* the defaults rather than into a zero value. yaml only
	// sets the fields a document mentions, so absent keys keep their default
	// and present ones overwrite it — which is what "every field is optional"
	// has to mean.
	//
	// Starting from zero worked for strings, where "" is distinguishable from a
	// real value and applyDefaults below fills it in. It does not work for a
	// bool: an absent `feed:` left it false, the opposite of the documented
	// default, and the menu bar reported "tun-manager is not running" while
	// tun-manager was running. `notify:` had the same hole for longer.
	cfg := Default()
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	// A key this program does not know is refused rather than ignored. yaml
	// ignores one by default, which is how somebody writes `feeed: false`,
	// watches nothing happen, and has no way at all to find out why. The cost
	// is that a configuration written for a newer tun-manager will not load on
	// an older one — which is a clear failure at startup naming the key, and
	// better than the setting quietly not applying.
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		// io.EOF is an empty file: somebody created it and has not filled it in.
		if moved := movedKey(err); moved != "" {
			return nil, fmt.Errorf("%s: %s", path, movedKeys[moved])
		}
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.Path = path
	cfg.applyDefaults()
	return cfg, nil
}

// movedKeys are the settings that used to live in the user's file and no longer
// can. Each one decided something root would then do, from a file a plain user
// can write.
//
// Refusing them is not pedantry: somebody upgrading has a file that used to
// work, and the alternative is a setting that silently stops applying. The
// message is the whole feature, so it says what moved, where to, and why.
var movedKeys = map[string]string{
	"config_dir": "config_dir is no longer a setting. The .conf files are read from " +
		ConfigDir + " and from nowhere else, because a directory named by a file " +
		"a plain user can write is a directory that user can point at .conf files " +
		"of their own — and wg-quick runs those as root. Remove the key. " +
		"Everything of that kind now lives in " + PrivilegedPath + ".",
	"wg_quick":    movedToRoot("wg_quick", "it names the binary tun-manager executes as root"),
	"run_dir":     movedToRoot("run_dir", "it names the directory tun-manager believes the live state is in"),
	"feed":        movedToRoot("feed", "it decides whether root binds a socket at all"),
	"feed_socket": movedToRoot("feed_socket", "it names a path root unlinks before binding it"),
	"notify": "notify is no longer a setting. tun-manager posts no notifications " +
		"itself: doing so meant a program running as root starting a GUI process " +
		"under somebody else's identity, for a banner. Tun Manager.app posts them, " +
		"from a session that is already the right one - it has a Test Notification " +
		"button in its About panel. Remove the key.",
}

// movedToRoot writes the sentence somebody upgrading will read. One shape for
// all of them: what moved, where to, why it could not stay, and what to type.
func movedToRoot(key, because string) string {
	return key + " has moved to " + PrivilegedPath + ", which only root can write, because " +
		because + " — and a setting a plain user can write is a setting any program running as " +
		"that user can write, which the next `sudo tun-manager` would then honour. " +
		"Move the key there (see configs/tun-manager.example.yaml) and remove it here."
}

// movedKey reports which moved setting a decode error is about, or "" when it
// is about something else.
//
// It matches on the message yaml produces for an unknown field, which is the
// only thing it gives us: KnownFields reports "field <name> not found in type
// …". A yaml that changed that wording would cost the friendly message, not the
// refusal — the key would still be unknown, and the file would still be
// rejected.
func movedKey(err error) string {
	message := err.Error()
	for key := range movedKeys {
		if strings.Contains(message, "field "+key+" not found") {
			return key
		}
	}
	return ""
}

// applyDefaults fills anything a document set to an empty value. Load starts
// from Default(), so this only catches keys written out as blank rather than
// left out.
func (c *Config) applyDefaults() {
	d := Default()
	if c.ConfigDir == "" {
		c.ConfigDir = d.ConfigDir
	}
	switch {
	case c.RefreshInterval <= 0:
		// Not asked for at all, or written out as blank: the default, and
		// nothing to report.
		c.RefreshInterval = d.RefreshInterval
	case c.RefreshInterval < MinRefresh:
		c.RefreshRaisedFrom = c.RefreshInterval
		c.RefreshInterval = MinRefresh
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
