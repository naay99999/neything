package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/lockfile"
	"github.com/naay99999/neything/internal/store"
	"github.com/spf13/cobra"
)

var resetForce bool

var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Clear the index",
	RunE:  runReset,
}

func init() {
	resetCmd.Flags().BoolVar(&resetForce, "force", false, "skip confirmation prompt")
}

type resetResult struct {
	OK        bool   `json:"ok"`
	Aborted   bool   `json:"aborted,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Workspace string `json:"workspace,omitempty"`
}

func runReset(cmd *cobra.Command, args []string) error {
	if !resetForce {
		if strings.ToLower(promptLine("This will delete all indexed data. Continue? [y/N] ")) != "y" {
			if flagJSON {
				PrintJSON(resetResult{OK: false, Aborted: true})
				return nil
			}
			fmt.Println(Yellow("Aborted."))
			return nil
		}
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	lock, err := lockfile.Acquire(config.NeyDir())
	if err != nil {
		return err
	}
	defer lock.Release()

	db, err := store.Open(config.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()

	vs, err := config.NewVectorStore(cfg, db, false)
	if err != nil {
		return err
	}
	defer vs.Close()

	if flagWorkspace != "" {
		ws, err := db.GetWorkspaceByName(flagWorkspace)
		if err != nil {
			return err
		}
		if ws == nil {
			return fmt.Errorf("workspace %q not found", flagWorkspace)
		}
		chunkIDs, err := db.GetChunkIDsByWorkspace(ws.ID)
		if err != nil {
			return err
		}
		if err := vs.Delete(cmd.Context(), store.Int64SliceToStrings(chunkIDs)); err != nil {
			return err
		}
		if err := vs.Flush(); err != nil {
			return err
		}
		if err := db.DeleteWorkspace(ws.ID); err != nil {
			return err
		}
		if flagJSON {
			PrintJSON(resetResult{OK: true, Scope: "workspace", Workspace: flagWorkspace})
			return nil
		}
		fmt.Println(Green(fmt.Sprintf("✓ Reset workspace %q", flagWorkspace)))
		return nil
	}

	if err := db.DeleteAllData(); err != nil {
		return err
	}
	os.Remove(config.VectorsPath())
	os.Remove(config.HNSWPath())
	os.Remove(config.HNSWPath() + ".graph")

	if flagJSON {
		PrintJSON(resetResult{OK: true, Scope: "full"})
		return nil
	}
	fmt.Println(Green("✓ Index cleared"))
	return nil
}
