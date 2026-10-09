package main

import (
	"fmt"
	"github.com/spf13/cobra"
	"kitsoki/internal/tourspec"
)

func tourSpecCmd() *cobra.Command {
	c := &cobra.Command{Use: "tour-spec", Short: "Validate and compile semantic TourSpec documents"}
	c.AddCommand(&cobra.Command{Use: "validate FILE", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, a []string) error {
		s, e := tourspec.Load(a[0])
		if e != nil {
			return e
		}
		r := s.Validate()
		for _, x := range r.Errors {
			fmt.Fprintln(cmd.ErrOrStderr(), x)
		}
		if !r.OK {
			return fmt.Errorf("TourSpec validation failed")
		}
		fmt.Fprintln(cmd.OutOrStdout(), "TourSpec valid")
		return nil
	}})
	return c
}
