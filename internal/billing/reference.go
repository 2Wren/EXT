package billing

import "database/sql"

// Rates are nanoUSD/token, i.e. USD per million tokens multiplied by 1000.
// These are reference estimates, never certified quota bounds.
func SeedReferences(db *sql.DB) error {
	type rate struct {
		model                        string
		input, output, cached, write int64
		source, url, note            string
		context                      int64
	}
	const openai = "https://developers.openai.com/api/docs/pricing"
	const claude = "https://platform.claude.com/docs/en/about-claude/pricing"
	rates := []rate{
		{"gpt-4o", 2500, 10000, 1250, 0, "OpenAI", openai, "Standard text token rates; excludes tools and special tiers.", 128000},
		{"gpt-4o-mini", 150, 600, 75, 0, "OpenAI", openai, "Standard text token rates; excludes tools and special tiers.", 128000},
		{"gpt-4.1", 2000, 8000, 500, 0, "OpenAI", openai, "Standard text token rates; excludes tools and special tiers.", 1000000},
		{"gpt-4.1-mini", 400, 1600, 100, 0, "OpenAI", openai, "Standard text token rates; excludes tools and special tiers.", 1000000},
		{"gpt-5", 1250, 10000, 125, 0, "OpenAI", openai, "Standard text token rates; excludes tools and special tiers.", 272000},
		{"gpt-5-mini", 250, 2000, 25, 0, "OpenAI", openai, "Standard text token rates; excludes tools and special tiers.", 272000},
		{"gpt-5-nano", 50, 400, 5, 0, "OpenAI", openai, "Standard text token rates; excludes tools and special tiers.", 272000},
		{"gpt-5.1", 1250, 10000, 125, 0, "OpenAI", openai, "Standard text token rates; excludes tools and special tiers.", 272000},
		{"gpt-5.2", 1750, 14000, 175, 0, "OpenAI", openai, "Standard text token rates; excludes tools and special tiers.", 272000},
		{"gpt-5.3-codex", 1750, 14000, 175, 0, "OpenAI", openai, "Standard text token rates; excludes tools and special tiers.", 272000},
		{"gpt-5.4", 2500, 15000, 250, 0, "OpenAI", openai, "Short context <=272K, standard tier only; excludes regional uplifts.", 272000},
		{"gpt-5.4-mini", 750, 4500, 75, 0, "OpenAI", openai, "Standard text token rates; excludes tools and special tiers.", 272000},
		{"gpt-5.4-nano", 200, 1250, 20, 0, "OpenAI", openai, "Standard text token rates; excludes tools and special tiers.", 272000},
		{"gpt-5.5", 5000, 30000, 500, 0, "OpenAI", openai, "Short context <=272K, standard tier only; excludes regional uplifts.", 272000},
		{"gpt-6.1-sol", 2000, 10000, 100, 2500, "OpenAI", openai, "Short context <=272K, standard tier, 30m cache write estimate only; excludes other TTLs and uplifts.", 272000},
		{"gpt-6.2-sol", 2000, 10000, 100, 2500, "OpenAI", openai, "Short context <=272K, standard tier, 30m cache write estimate only; excludes other TTLs and uplifts.", 272000},
		{"gpt-6.2-astra", 8000, 40000, 400, 10000, "OpenAI", openai, "Short context <=272K, standard tier, 30m cache write estimate only; excludes other TTLs and uplifts.", 272000},
		{"claude-sonnet-4-5", 3000, 15000, 300, 3750, "Anthropic", claude, "Standard text <=200K, 5m cache write estimate only; 1h writes/tools are not represented.", 200000},
		{"claude-sonnet-4-6", 3000, 15000, 300, 3750, "Anthropic", claude, "Standard text <=200K, 5m cache write estimate only; 1h writes/tools are not represented.", 200000},
		{"claude-opus-4-5", 5000, 25000, 500, 6250, "Anthropic", claude, "Standard text <=200K, 5m cache write estimate only; 1h writes/tools are not represented.", 200000},
		{"claude-opus-4-6", 5000, 25000, 500, 6250, "Anthropic", claude, "Standard text <=200K, 5m cache write estimate only; 1h writes/tools are not represented.", 200000},
		{"claude-haiku-4-5", 1000, 5000, 100, 1250, "Anthropic", claude, "Standard text <=200K, 5m cache write estimate only; 1h writes/tools are not represented.", 200000},
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range rates {
		for _, protocol := range []string{"chat", "responses", "responses-ws", "messages", "gemini"} {
			_, err = tx.Exec(`INSERT INTO prices(protocol,model,billed_model,input,output,cached,cache_write,max_input,max_output,verified,source,source_url,source_date,is_reference,note)
   SELECT ?,?,?,?,?,?,?,?,?,0,?,?,?,1,? WHERE NOT EXISTS(SELECT 1 FROM prices WHERE protocol=? AND model=? AND is_reference=1 AND source_date=?)`,
				protocol, r.model, r.model, r.input, r.output, r.cached, r.write, r.context, int64(1000000), r.source, r.url, "2026-11-21", r.note, protocol, r.model, "2026-11-21")
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
