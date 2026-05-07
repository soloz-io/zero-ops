// Atlas configuration for PostgreSQL migrations
// https://atlasgo.io/

env "local" {
  // Database URL for local development
  url = getenv("DATABASE_URL")
  
  // Migration directory
  migration {
    dir = "file://migrations"
  }
  
  // Schema source
  src = "file://migrations"
  
  // Development database for schema diffing
  dev = "docker://postgres/15/dev?search_path=public"
}

env "production" {
  // Database URL from environment
  url = getenv("DATABASE_URL")
  
  // Migration directory
  migration {
    dir = "file://migrations"
  }
  
  // Schema source
  src = "file://migrations"
  
  // Format for migration files
  format {
    migrate {
      diff = "{{ sql . \"  \" }}"
    }
  }
}
