APP=WizCamera
IDENTIFIER=com.github.caseymrm.wizcamera

# menuet ships a shared Makefile that assembles the .app bundle; find it in
# the module cache since we're using Go modules rather than GOPATH.
MENUET_DIR=$(shell go list -m -f '{{.Dir}}' github.com/caseymrm/menuet)
include $(MENUET_DIR)/menuet.mk
