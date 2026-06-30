package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/naay/ney/internal/config"
	"github.com/naay/ney/internal/store"
	"github.com/naay/ney/internal/vectorstore"
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

func runReset(cmd *cobra.Command, args []string) error {
	if !resetForce {
		fmt.Print("This will delete all indexed data. Continue? [y/N] ")
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		if strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	db, err := store.Open(config.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()

	vs, err := vectorstore.NewBruteForceStore(config.VectorsPath())
	if err != nil {
		return err
	}

	if flagWorkspace != "" {
		ws, err := db.GetWorkspaceByName(flagWorkspace)
		if err != nil {
			return err
		}
		if ws == nil {
			return fmt.Errorf("workspace %q not found", flagWorkspace)
		}
		// get chunk IDs to delete from vector store
		chunkIDs, err := db.GetChunkIDsByWorkspace(ws.ID)
		if err != nil {
			return err
		}
		if err := vs.Delete(cmd.Context(), store.Int64SliceToStrings(chunkIDs)); err != nil {
			return err
		}
		if err := db.DeleteWorkspace(ws.ID); err != nil {
			return err
		}
		fmt.Printf("✓ Reset workspace %q\n", flagWorkspace)
		return nil
	}

	// full reset
	if err := db.DeleteAllData(); err != nil {
		return err
	}
	// delete vectors file
	os.Remove(config.VectorsPath())

	fmt.Println("✓ Index cleared")
	return nil
}
