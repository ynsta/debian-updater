# Workaround for Net::SSH 7.3.0 + Ruby 3.3 FrozenError
# Ensure Net::SSH::Buffer content is always mutable
begin
  require 'net/ssh/buffer'
  module Net
    module SSH
      class Buffer
        def initialize(content = String.new)
          @content = content.to_s.dup
          @position = 0
        end
      end
    end
  end
rescue LoadError
end

Vagrant.configure("2") do |config|
  config.vm.box = "generic/debian10"

  config.vm.provider :libvirt do |libvirt|
    libvirt.memory = 2048
    libvirt.cpus = 2
  end

  # Sync the current directory to /debian-updater in the guest
  config.vm.synced_folder ".", "/debian-updater", type: "rsync"

  config.vm.provision "shell", inline: <<-SHELL
    echo "==> Preparing system for upgrade testing..."
    
    # Fix grub-pc pointing to non-existent disk IDs in this specific box
    # This prevents the 'grub-pc' package from failing during upgrade
    echo "grub-pc grub-pc/install_devices multiselect /dev/vda" | debconf-set-selections
    dpkg --configure -a

    # Ensure the binary is available and executable
    chmod +x /debian-updater/debian-updater
  SHELL
end
