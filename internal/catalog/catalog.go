// Package catalog lists the values the public Kuticon API accepts.
package catalog

// Looks is the set of drawing styles. The first is the default.
var Looks = []string{"outline", "sticker", "blueprint", "neon", "clay", "pixel"}

// Counts is the set of pack lengths. 1 is one icon.
var Counts = []int{1, 4, 6, 8, 12, 16, 24}

// Formats is every file kind a pack can carry.
var Formats = []string{"svg", "png", "font", "react", "vue", "sprite", "app"}

// Sizes is the set of PNG sides.
var Sizes = []int{16, 24, 32, 48, 64, 128, 256}
