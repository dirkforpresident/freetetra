package service

import (
	"sync"
	"time"
)

// AuthRateLimiter tracks failed authentication attempts and auto-bans repeat offenders.
type AuthRateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time // IP -> timestamps of failed attempts
	banned   map[string]time.Time   // IP -> ban expiry
}

const (
	maxAuthAttempts  = 5               // max failed attempts before ban
	authWindow       = 2 * time.Minute // window for counting attempts
	banDuration      = 15 * time.Minute // how long to ban
)

func newAuthRateLimiter() *AuthRateLimiter {
	return &AuthRateLimiter{
		attempts: make(map[string][]time.Time),
		banned:   make(map[string]time.Time),
	}
}

// IsBanned checks if an IP is currently banned.
func (rl *AuthRateLimiter) IsBanned(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	expiry, ok := rl.banned[ip]
	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		delete(rl.banned, ip)
		return false
	}
	return true
}

// RecordFailure records a failed auth attempt. Returns true if the IP is now banned.
func (rl *AuthRateLimiter) RecordFailure(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-authWindow)

	// Clean old attempts
	valid := make([]time.Time, 0)
	for _, t := range rl.attempts[ip] {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	valid = append(valid, now)
	rl.attempts[ip] = valid

	if len(valid) >= maxAuthAttempts {
		rl.banned[ip] = now.Add(banDuration)
		delete(rl.attempts, ip)
		return true
	}
	return false
}

// RecordSuccess clears failed attempts for an IP.
func (rl *AuthRateLimiter) RecordSuccess(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.attempts, ip)
}

// BanCount returns the number of currently banned IPs.
func (rl *AuthRateLimiter) BanCount() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	count := 0
	for ip, expiry := range rl.banned {
		if now.After(expiry) {
			delete(rl.banned, ip)
		} else {
			count++
		}
	}
	return count
}
