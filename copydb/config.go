package copydb

import (
	"fmt"

	"github.com/Shopify/ghostferry"
)

type Config struct {
	GhostferryConfig *ghostferry.Config

	ApplicableDatabases map[string]bool
	ApplicableTables    map[string]bool
}

func (c *Config) ValidateConfig() error {
	if len(c.ApplicableDatabases) == 0 {
		return fmt.Errorf("failed to validate config: no applicable databases specified")
	}

	c.GhostferryConfig.Applicability = NewStaticApplicableFilter(
		c.ApplicableDatabases,
		c.ApplicableTables,
	)

	if err := c.GhostferryConfig.ValidateConfig(); err != nil {
		return err
	}

	return nil
}
