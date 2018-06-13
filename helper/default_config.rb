require 'xcodeproj'
require 'json'

def workspace?(project_path)
  extname = File.extname(project_path)
  extname == '.xcworkspace'
end
  
def contained_projects(project_path)
  return [project_path] unless workspace?(project_path)

  workspace = Xcodeproj::Workspace.new_from_xcworkspace(project_path)
  workspace_dir = File.dirname(project_path)
  project_paths = []
  workspace.file_references.each do |ref|
    pth = ref.path
    next unless File.extname(pth) == '.xcodeproj'
    next if pth.end_with?('Pods/Pods.xcodeproj')

    project_path = File.expand_path(pth, workspace_dir)
    project_paths << project_path
  end

  project_paths
end

def launch_action_default_config(project_path, scheme_name)
  schemes_by_project = {}

  project_paths = contained_projects(project_path)
  project_paths.each do |path|
    scheme_path = File.join(path, 'xcshareddata', 'xcschemes', scheme_name + '.xcscheme')
    next unless File.exist?(scheme_path)

    scheme = Xcodeproj::XCScheme.new(scheme_path)

    action = scheme.launch_action
    next unless action

    configuration = action.build_configuration
    next unless configuration

    return configuration
  end

  raise 'launch action default configuration not found'
end
  
begin
  path = ENV['PROEJECTPATH']
  ret = launch_action_default_config(path, ENV['APP_NAME'])
  result = {
    data: ret
  }
  result_json = JSON.pretty_generate(result).to_s
  puts result_json
rescue => e
  error_message = e.to_s + "\n" + e.backtrace.join("\n")
  result = {
    error: error_message
  }
  result_json = result.to_json.to_s
  puts result_json
  exit(1)
end