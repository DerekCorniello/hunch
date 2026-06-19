package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"
)

func cmdUpdate() error {
	fmt.Println("hunch update")
	fmt.Println()
	fmt.Printf("current version: %s\n", Version)

	latest, err := fetchLatestVersion()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not check latest version: %v\n", err)
	} else {
		fmt.Printf("latest version:  %s\n", latest)
		if Version != "dev" && Version == latest {
			fmt.Println("\nAlready up to date.")
			return nil
		}
	}

	fmt.Println("\nInstalling latest version...")
	installCmd := exec.Command("go", "install", "github.com/DerekCorniello/hunch@latest")
	installCmd.Stdout = os.Stdout
	installCmd.Stderr = os.Stderr
	if err := installCmd.Run(); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	fmt.Println("\nUpdate complete.")
	fmt.Println("Restarting daemon...")
	if err := restartDaemon(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: restart daemon: %v\n", err)
		fmt.Println("Restart manually: hunch daemon stop && hunch daemon start")
	}
	return nil
}

func fetchLatestVersion() (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/DerekCorniello/hunch/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	return release.TagName, nil
}

func restartDaemon() error {
	if err := cmdDaemonStop(); err != nil {
		return fmt.Errorf("stop: %w", err)
	}
	if err := cmdDaemonStart(); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	return nil
}
