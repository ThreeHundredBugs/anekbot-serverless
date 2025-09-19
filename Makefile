.PHONY: test

test:
	pytest ./src/anekbotpy/

artifact_path ?= "$(CURDIR)/anekbot.zip"
build:
	cd ./src/anekbotpy && \
	zip -r "$(artifact_path)" . -x "*_tests.py" "*__pycache__*" "*pytest*" ".venv*" "test-*"
