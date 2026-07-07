APP=Tally
IDENTIFIER=com.github.caseymrm.tally

# menuet ships a shared Makefile that assembles the .app bundle; find it in
# the module cache since we're using Go modules rather than GOPATH.
MENUET_DIR=$(shell go list -m -f '{{.Dir}}' github.com/caseymrm/menuet/v2)
include $(MENUET_DIR)/menuet.mk
