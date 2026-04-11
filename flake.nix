{
  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    git-hooks.url = "github:cachix/git-hooks.nix";
    git-hooks.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs = {
    self,
    nixpkgs,
    git-hooks,
  }: let
    forAllSystems = f:
      nixpkgs.lib.genAttrs
      ["x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin"]
      (system: f nixpkgs.legacyPackages.${system});
  in {
    checks = forAllSystems (pkgs: {
      pre-commit = git-hooks.lib.${pkgs.system}.run {
        src = ./.;
        hooks = {
          gotest.enable = true;
          govet.enable = true;
          alejandra.enable = true;
          golangci-lint = {
            enable = true;
            name = "golangci-lint";
            entry = "${pkgs.golangci-lint}/bin/golangci-lint fmt";
            types = ["go"];
          };

          nix-build = {
            enable = true;
            name = "nix-build";
            entry = pkgs.lib.getExe (pkgs.writeShellApplication {
              name = "nix-build-check";
              runtimeInputs = [pkgs.nix];
              text = "nix build --no-link";
            });
            stages = ["pre-push"];
            pass_filenames = false;
            files = "go\\.(mod|sum)|flake\\.nix";
          };
        };
      };
    });

    packages = forAllSystems (pkgs: {
      default = pkgs.buildGoModule {
        pname = "null-core";
        version = self.shortRev or self.dirtyShortRev or "dev";
        src = ./.;
        vendorHash = "sha256-R0/pogg9IMjnKWuldCGcqb6UnSVUAM33de/S81gEUv4=";
        subPackages = ["cmd/null"];
      };
    });

    devShells = forAllSystems (pkgs: {
      default = pkgs.mkShell {
        packages = with pkgs; [
          go

          grpcurl
          buf
          goose
          sqlc
          air
          golangci-lint

          protoc-gen-go-grpc
          protoc-gen-connect-go
          protoc-gen-go

          (writeShellScriptBin "run" ''
            go run cmd/null/main.go
          '')

          (writeShellScriptBin "fmt" ''
            ${golangci-lint}/bin/golangci-lint fmt
          '')

          (writeShellScriptBin "tstv" ''
            CLICOLOR_FORCE=1 go test ./... -v
          '')

          (writeShellScriptBin "tst" ''
            go test ./...
          '')

          (writeShellScriptBin "migrate" ''
            ${goose}/bin/goose -dir internal/db/migrations postgres "$DATABASE_URL" up
          '')

          (writeShellScriptBin "bump-protos" ''
            git -C proto fetch origin
            git -C proto checkout main
            git -C proto pull --ff-only
            git add proto
            git commit -m "chore: bump proto files"
            git push
          '')

          (writeShellScriptBin "regen" ''
            rm -rf internal/db/sqlc/
            ${sqlc}/bin/sqlc generate
            rm -rf internal/gen/
            ${buf}/bin/buf generate
          '')

          (writeShellScriptBin "cover" ''
            go test -coverprofile=coverage.out ./... && \
            go tool cover -html=coverage.out -o coverage.html
          '')

          (writeShellScriptBin "test-db" ''
            # run database tests. uses DATABASE_URL if TEST_DATABASE_URL not set.
            if [ -z "$TEST_DATABASE_URL" ]; then
              TEST_DATABASE_URL="$DATABASE_URL"
            fi
            if [ -z "$TEST_DATABASE_URL" ]; then
              echo "Error: Set TEST_DATABASE_URL or DATABASE_URL"
              exit 1
            fi
            TEST_DATABASE_URL="$TEST_DATABASE_URL" go test ./internal/db/... -v
          '')
        ];

        shellHook = "${self.checks.${pkgs.system}.pre-commit.shellHook}";
      };
    });
  };
}
