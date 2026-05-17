package main

import (
	"fmt"
	"os"
	"strings"
)

// writeSQL emits a small mysqldump-shape file containing the WordPress
// schema this site exercises plus a handful of representative rows.
//
// This is a *shape* artefact: enough INSERTs that a `dbdirect` importer
// test can stub out a MySQL via a parser fixture without us hauling around
// a full database snapshot. Production importer tests are expected to
// re-import these into a real MariaDB during nightly CI; the daily fast
// path can parse this file directly.
func writeSQL(path string, s *site) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	b := &strings.Builder{}
	fmt.Fprintf(b, "-- gonext-corpus synthetic dump\n")
	fmt.Fprintf(b, "-- site: %s (profile=%s)\n", s.Title, s.Profile.Slug)
	fmt.Fprintf(b, "-- generated: %s (deterministic)\n", s.Generated.UTC().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(b, "-- WARNING: synthetic data only. Not from any real site.\n\n")

	b.WriteString(`SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS = 0;

DROP TABLE IF EXISTS ` + "`wp_users`" + `;
CREATE TABLE ` + "`wp_users`" + ` (
  ID bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  user_login varchar(60) NOT NULL DEFAULT '',
  user_pass varchar(255) NOT NULL DEFAULT '',
  user_email varchar(100) NOT NULL DEFAULT '',
  user_registered datetime NOT NULL DEFAULT '0000-00-00 00:00:00',
  display_name varchar(250) NOT NULL DEFAULT '',
  PRIMARY KEY (ID)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

DROP TABLE IF EXISTS ` + "`wp_terms`" + `;
CREATE TABLE ` + "`wp_terms`" + ` (
  term_id bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  name varchar(200) NOT NULL DEFAULT '',
  slug varchar(200) NOT NULL DEFAULT '',
  PRIMARY KEY (term_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

DROP TABLE IF EXISTS ` + "`wp_term_taxonomy`" + `;
CREATE TABLE ` + "`wp_term_taxonomy`" + ` (
  term_taxonomy_id bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  term_id bigint(20) unsigned NOT NULL DEFAULT 0,
  taxonomy varchar(32) NOT NULL DEFAULT '',
  parent bigint(20) unsigned NOT NULL DEFAULT 0,
  count bigint(20) NOT NULL DEFAULT 0,
  PRIMARY KEY (term_taxonomy_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

DROP TABLE IF EXISTS ` + "`wp_posts`" + `;
CREATE TABLE ` + "`wp_posts`" + ` (
  ID bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  post_author bigint(20) unsigned NOT NULL DEFAULT 0,
  post_date datetime NOT NULL DEFAULT '0000-00-00 00:00:00',
  post_content longtext NOT NULL,
  post_title text NOT NULL,
  post_excerpt text NOT NULL,
  post_status varchar(20) NOT NULL DEFAULT 'publish',
  post_name varchar(200) NOT NULL DEFAULT '',
  post_parent bigint(20) unsigned NOT NULL DEFAULT 0,
  guid varchar(255) NOT NULL DEFAULT '',
  post_type varchar(20) NOT NULL DEFAULT 'post',
  PRIMARY KEY (ID)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

DROP TABLE IF EXISTS ` + "`wp_postmeta`" + `;
CREATE TABLE ` + "`wp_postmeta`" + ` (
  meta_id bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  post_id bigint(20) unsigned NOT NULL DEFAULT 0,
  meta_key varchar(255) DEFAULT NULL,
  meta_value longtext,
  PRIMARY KEY (meta_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

DROP TABLE IF EXISTS ` + "`wp_comments`" + `;
CREATE TABLE ` + "`wp_comments`" + ` (
  comment_ID bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  comment_post_ID bigint(20) unsigned NOT NULL DEFAULT 0,
  comment_author tinytext NOT NULL,
  comment_author_email varchar(100) NOT NULL DEFAULT '',
  comment_date datetime NOT NULL DEFAULT '0000-00-00 00:00:00',
  comment_content text NOT NULL,
  comment_approved varchar(20) NOT NULL DEFAULT '1',
  comment_parent bigint(20) unsigned NOT NULL DEFAULT 0,
  PRIMARY KEY (comment_ID)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

DROP TABLE IF EXISTS ` + "`wp_options`" + `;
CREATE TABLE ` + "`wp_options`" + ` (
  option_id bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  option_name varchar(191) NOT NULL DEFAULT '',
  option_value longtext NOT NULL,
  autoload varchar(20) NOT NULL DEFAULT 'yes',
  PRIMARY KEY (option_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

`)

	// Users.
	for _, a := range s.Authors {
		fmt.Fprintf(b,
			"INSERT INTO `wp_users` (ID, user_login, user_pass, user_email, user_registered, display_name) VALUES (%d, %s, %s, %s, %s, %s);\n",
			a.ID, sqlString(a.Login), sqlString("$P$Bsynthetic-not-a-real-hash"),
			sqlString(a.Email), sqlString(s.Generated.UTC().Format("2006-01-02 15:04:05")),
			sqlString(a.DisplayName),
		)
	}
	b.WriteString("\n")

	// Terms + taxonomies.
	for _, t := range s.Terms {
		fmt.Fprintf(b, "INSERT INTO `wp_terms` (term_id, name, slug) VALUES (%d, %s, %s);\n",
			t.ID, sqlString(t.Name), sqlString(t.Slug))
		fmt.Fprintf(b, "INSERT INTO `wp_term_taxonomy` (term_taxonomy_id, term_id, taxonomy, parent, count) VALUES (%d, %d, %s, %d, 0);\n",
			t.ID, t.ID, sqlString(t.Taxonomy), t.ParentID)
	}
	b.WriteString("\n")

	// Posts: emit a representative sample (cap at 25 INSERTs to keep the
	// file small; full row fidelity is the WXR's job).
	postCap := 25
	if len(s.Posts) < postCap {
		postCap = len(s.Posts)
	}
	for i := 0; i < postCap; i++ {
		p := s.Posts[i]
		fmt.Fprintf(b,
			"INSERT INTO `wp_posts` (ID, post_author, post_date, post_content, post_title, post_excerpt, post_status, post_name, post_parent, guid, post_type) VALUES (%d, %d, %s, %s, %s, %s, %s, %s, %d, %s, %s);\n",
			p.ID, p.AuthorID,
			sqlString(p.Date.UTC().Format("2006-01-02 15:04:05")),
			sqlString(p.Content), sqlString(p.Title), sqlString(p.Excerpt),
			sqlString(p.Status), sqlString(p.Slug), p.ParentID,
			sqlString(fmt.Sprintf("%s/?p=%d", s.BaseURL, p.ID)),
			sqlString(p.Type),
		)
		for _, m := range p.Postmeta {
			fmt.Fprintf(b,
				"INSERT INTO `wp_postmeta` (post_id, meta_key, meta_value) VALUES (%d, %s, %s);\n",
				p.ID, sqlString(m.Key), sqlString(m.Value),
			)
		}
		for _, c := range p.Comments {
			fmt.Fprintf(b,
				"INSERT INTO `wp_comments` (comment_ID, comment_post_ID, comment_author, comment_author_email, comment_date, comment_content, comment_approved, comment_parent) VALUES (%d, %d, %s, %s, %s, %s, %s, %d);\n",
				c.ID, p.ID, sqlString(c.Author), sqlString(c.Email),
				sqlString(c.Date.UTC().Format("2006-01-02 15:04:05")),
				sqlString(c.Content), sqlString(c.Approved), c.ParentID,
			)
		}
	}
	b.WriteString("\n")

	// Options.
	for _, o := range s.OptionsRows {
		fmt.Fprintf(b,
			"INSERT INTO `wp_options` (option_name, option_value, autoload) VALUES (%s, %s, 'yes');\n",
			sqlString(o.Name), sqlString(o.Value),
		)
	}

	b.WriteString("\nSET FOREIGN_KEY_CHECKS = 1;\n")

	_, err = f.WriteString(b.String())
	return err
}

// sqlString escapes a Go string into a single-quoted SQL literal. This is
// intentionally simple: the corpus is synthetic and contains only
// well-behaved characters, but we still escape backslashes, single quotes,
// and newlines so the output parses under stricter SQL clients.
func sqlString(s string) string {
	b := &strings.Builder{}
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\'':
			b.WriteString("''")
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case 0:
			b.WriteString("\\0")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}
