package jobs

import (
	"log"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"s3gate/db"
)

// StartScheduler runs background jobs
func StartScheduler() {
	go func() {
		// Run immediately on startup, then daily
		time.Sleep(10 * time.Second)
		runAll()

		ticker := time.NewTicker(24 * time.Hour)
		for range ticker.C {
			runAll()
		}
	}()
	log.Println("Background job scheduler started")
}

func runAll() {
	log.Println("Running scheduled jobs...")
	expireTrials()
	monthlyBilling()
	markForDeletion()
	// Usage calculation runs separately (every 6 hours)
	go calculateUsage()
}

// expireTrials marks expired trial accounts
func expireTrials() {
	result, err := db.DB.Exec(`UPDATE users SET status = 'expired' WHERE status = 'trial' AND trial_expires_at < datetime('now')`)
	if err != nil {
		log.Printf("ERROR expiring trials: %v", err)
		return
	}
	n, _ := result.RowsAffected()
	if n > 0 {
		log.Printf("Jobs: expired %d trial accounts", n)
	}
}

// monthlyBilling deducts ₹99 from active users on their billing date
func monthlyBilling() {
	rows, err := db.DB.Query(`SELECT id FROM users WHERE status = 'active'`)
	if err != nil {
		log.Printf("ERROR monthly billing query: %v", err)
		return
	}

	var userIDs []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		userIDs = append(userIDs, id)
	}
	rows.Close()

	var billed, suspended int
	for _, userID := range userIDs {
		// Check if already billed this month
		month := time.Now().Format("2006-01")
		var count int
		db.DB.QueryRow(`SELECT COUNT(*) FROM transactions WHERE user_id = ? AND type = 'monthly_deduction' AND created_at >= ?`,
			userID, month+"-01").Scan(&count)
		if count > 0 {
			continue // Already billed this month
		}

		// Try to debit ₹99 (9900 paise)
		err := db.DebitWallet(userID, 9900, "Monthly storage - "+time.Now().Format("Jan 2006"))
		if err != nil {
			// Insufficient balance — suspend
			db.DB.Exec(`UPDATE users SET status = 'suspended' WHERE id = ?`, userID)
			suspended++
		} else {
			billed++
		}
	}

	if billed > 0 || suspended > 0 {
		log.Printf("Jobs: billed %d users, suspended %d (insufficient balance)", billed, suspended)
	}
}

// markForDeletion sets deletion date for suspended/expired users after 7 days
func markForDeletion() {
	result, err := db.DB.Exec(`UPDATE users SET data_deletion_at = datetime('now', '+7 days') 
		WHERE status IN ('suspended', 'expired') AND data_deletion_at IS NULL`)
	if err != nil {
		log.Printf("ERROR marking for deletion: %v", err)
		return
	}
	n, _ := result.RowsAffected()
	if n > 0 {
		log.Printf("Jobs: marked %d accounts for data deletion in 7 days", n)
	}
}

// calculateUsage measures storage per user via rclone
func calculateUsage() {
	rows, err := db.DB.Query(`SELECT id FROM users WHERE status IN ('active', 'trial')`)
	if err != nil {
		log.Printf("ERROR usage query: %v", err)
		return
	}

	var userIDs []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		userIDs = append(userIDs, id)
	}
	rows.Close()

	for _, userID := range userIDs {
		// Use rclone to calculate size of user's prefix
		cmd := exec.Command("rclone", "size", "storagebox:./", "--include", userID+"--**", "--json")
		output, err := cmd.Output()
		if err != nil {
			continue
		}

		// Parse JSON output: {"count":N,"bytes":N}
		var sizeBytes int64
		for _, part := range strings.Split(string(output), ",") {
			if strings.Contains(part, "\"bytes\"") {
				numStr := strings.TrimSpace(strings.Split(part, ":")[1])
				numStr = strings.TrimRight(numStr, "}")
				sizeBytes, _ = strconv.ParseInt(numStr, 10, 64)
			}
		}

		db.RecordUsage(userID, sizeBytes)
	}

	log.Println("Jobs: usage calculation complete")
}
